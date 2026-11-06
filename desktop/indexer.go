package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// indexStatus — что окно показывает о сборке индекса.
type indexStatus struct {
	Building bool `json:"building"`
	Indexed  int  `json:"indexed"`
	Skipped  int  `json:"skipped"`
	// SkippedFiles — какие файлы не прочитаны и почему: панель «Не прочитано»
	// по кнопке «подробнее» (первые 100, Skipped считает все).
	SkippedFiles []skippedRow `json:"skippedFiles,omitempty"`
	Reused       int          `json:"reused"` // взяты из снимка без чтения
	Error        string       `json:"error,omitempty"`
	// Recognizing — идет проход распознавания PDF и картинок: сколько из
	// скольких. Первый проход длится десятки минут, и без счета он неотличим
	// от зависания.
	Recognizing *recognizeProgress `json:"recognizing,omitempty"`
}

// recognizeProgress is how far the recognition pass has got.
type recognizeProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// indexer собирает индекс файлов в фоне и пересобирает его при смене папок.
//
// Источник (открытые корни) принадлежит ему: новый подменяет старый только
// после удачной сборки, старый закрывается тогда же, а при неудаче закрывается
// новый — поиск все это время отвечает по прежнему индексу.
type indexer struct {
	files *filesearch.Service

	// build делает сборки последовательными: иначе ранняя сборка, закончив
	// позже поздней, подменила бы новые папки старыми.
	build sync.Mutex

	mu      sync.Mutex
	ctx     context.Context // живет, пока живет окно
	src     *localfiles.Source
	pending *localfiles.Source // источник, с которым идет сборка
	gen     int                // номер последней запущенной сборки
	status  indexStatus

	every   time.Duration  // как часто индекс догоняет диск (rebuildEvery)
	ext     extraction     // распознавание PDF и картинок (extract.go)
	catalog *catalogSwitch // база знаний, которую читает окно сейчас
	// reconcile — перед плановой пересборкой вернуть в индекс папки, что
	// снова доступны (folderSettings.reconcile); true — уже пересобирается.
	reconcile  func() bool
	extracting atomic.Bool                       // идет проход распознавания: второй не запускается
	progress   atomic.Pointer[recognizeProgress] // nil — прохода нет
	// retryTimeouts — пауза перед новой попыткой файла, на котором помощник
	// не уложился в таймаут; timedOut — когда это случилось. Трогает только
	// горутина прохода, а проходы не идут одновременно (extracting).
	retryTimeouts time.Duration
	timedOut      map[string]time.Time
}

// rebuildEvery — как часто окно догоняет диск, тот же срок, что у MCP: сборка
// инкрементальная (~0,5 с на 3 тыс. файлов из снимка), а после нее проход
// распознает новые PDF и картинки.
const rebuildEvery = 10 * time.Minute

func newIndexer(files *filesearch.Service, src *localfiles.Source) *indexer {
	return &indexer{files: files, src: src, pending: src, ctx: context.Background(), every: rebuildEvery,
		retryTimeouts: time.Hour, timedOut: map[string]time.Time{}}
}

// start runs the first build with the source the window started with.
func (x *indexer) start(ctx context.Context) {
	x.mu.Lock()
	x.ctx = ctx
	src, every := x.pending, x.every
	x.mu.Unlock()
	x.run(src)
	// Прежде индекс собирался только при старте и смене папок: файл,
	// сохраненный после запуска окна, не находился до перезапуска.
	//
	// ponytail: свое расписание рядом с расписанием MCP (cmd/kbengine-mcp
	// refresh), политики могут разойтись. Потолок: третья поверхность с
	// пересборкой. Путь: общий планировщик в usecase/filesearch.
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if x.reconcile != nil && x.reconcile() {
					continue // папки сменились, сборка уже запущена
				}
				if src := x.currentSource(); src != nil {
					x.rerun(src) // только если папки с тех пор не сменились
				}
			}
		}
	}()
}

// apply opens the new folders and reindexes in the background.
func (x *indexer) apply(roots, exclude []string) error {
	src, err := localfiles.New(roots, exclude, x.ext.option())
	if err != nil {
		return err
	}
	x.run(src)
	return nil
}

// applySources opens the new folders, then switches the catalog: каталог
// меняется, только если папки открылись, — иначе окно читало бы новую базу при
// прежних папках и прежнем конфиге.
func (x *indexer) applySources(roots, exclude []string, catalog string) error {
	if err := x.apply(roots, exclude); err != nil {
		return err
	}
	x.catalog.set(catalog)
	return nil
}

func (x *indexer) run(src *localfiles.Source) { x.launch(src, false) }

// rerun rebuilds over src only if no other folders were chosen since: проход
// распознавания не должен пересобирать индекс на старых папках (ревью #379).
func (x *indexer) rerun(src *localfiles.Source) { x.launch(src, true) }

// latest reports whether src is the source of the latest requested build.
func (x *indexer) latest(src *localfiles.Source) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.pending == src
}

func (x *indexer) launch(src *localfiles.Source, onlyLatest bool) {
	x.mu.Lock()
	if onlyLatest && x.pending != src {
		x.mu.Unlock()
		return
	}
	x.gen++
	gen, ctx := x.gen, x.ctx
	x.pending = src
	x.status.Building, x.status.Error = true, ""
	x.mu.Unlock()

	go func() {
		x.build.Lock()
		defer x.build.Unlock()
		if x.superseded(gen) {
			if src != x.currentSource() {
				_ = src.Close() // пока ждали, владелец выбрал другие папки
			}
			return
		}
		start := time.Now()
		st, err := x.files.Replace(ctx, src)
		if st.Warning != "" {
			log.Printf("files: %s", st.Warning)
		}
		if errors.Is(err, filesearch.ErrSaveFailed) {
			log.Printf("files: %v", err) // индекс подменен и работает, не записан только снимок
			err = nil
		}
		x.finish(ctx, gen, src, st, err, start)
	}()
}

// finish records a build that ended: подменяет источник при удаче, закрывает
// лишний при неудаче и запускает распознавание по новому индексу.
func (x *indexer) finish(ctx context.Context, gen int, src *localfiles.Source, st filesearch.Stats, err error, start time.Time) {
	x.mu.Lock()
	defer x.mu.Unlock()
	switch {
	case err != nil:
		log.Printf("files: %v", err)
		if src != x.src {
			_ = src.Close()
			// Последним снова считается рабочий источник: иначе rerun по
			// таймеру не запускался бы больше никогда (ревью #381).
			if x.pending == src {
				x.pending = x.src
			}
		}
		if gen == x.gen {
			x.status = indexStatus{Indexed: x.status.Indexed, Skipped: x.status.Skipped, SkippedFiles: x.status.SkippedFiles, Error: err.Error()}
		}
	default:
		log.Printf("files: indexed %d (skipped %d, reused %d) in %s", st.Indexed, st.Skipped, st.Reused, time.Since(start).Round(time.Millisecond))
		if old := x.src; old != nil && old != src {
			// Не сразу: поиск и чтение, взявшие старый источник до подмены,
			// должны успеть дочитать (ревью #372).
			// ponytail: задержка вместо счетчика читателей. Потолок: чтение
			// дольше минуты. Путь: счетчик ссылок на источник в Service.
			time.AfterFunc(time.Minute, func() { _ = old.Close() })
		}
		x.src = src
		if gen == x.gen {
			x.status = indexStatus{Indexed: st.Indexed, Skipped: st.Skipped, SkippedFiles: skippedRows(st.SkippedFiles), Reused: st.Reused}
		}
		x.extractPending(ctx, src) // в своей горутине: замок не держит
	}
}

// Status returns the state of the latest build and of the recognition pass.
func (x *indexer) Status() indexStatus {
	x.mu.Lock()
	st := x.status
	x.mu.Unlock()
	st.Recognizing = x.progress.Load()
	return st
}

func (x *indexer) superseded(gen int) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return gen != x.gen
}

func (x *indexer) currentSource() *localfiles.Source {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.src
}

// skippedRow is one unreadable file as the window shows it: причина словами
// человека (localfiles.Explain) и исходная ошибка — по ней видно, что именно.
type skippedRow struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

func skippedRows(files []filesearch.SkippedFile) []skippedRow {
	out := make([]skippedRow, 0, len(files))
	for _, f := range files {
		out = append(out, skippedRow{Path: f.Path, Reason: localfiles.Explain(f.Err), Detail: f.Err.Error()})
	}
	return out
}
