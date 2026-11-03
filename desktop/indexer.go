package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// indexStatus — что окно показывает о сборке индекса.
type indexStatus struct {
	Building bool   `json:"building"`
	Indexed  int    `json:"indexed"`
	Skipped  int    `json:"skipped"`
	Reused   int    `json:"reused"` // взяты из снимка без чтения
	Error    string `json:"error,omitempty"`
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
}

func newIndexer(files *filesearch.Service, src *localfiles.Source) *indexer {
	return &indexer{files: files, src: src, pending: src, ctx: context.Background()}
}

// start runs the first build with the source the window started with.
func (x *indexer) start(ctx context.Context) {
	x.mu.Lock()
	x.ctx = ctx
	src := x.pending
	x.mu.Unlock()
	x.run(src)
}

// apply opens the new folders and reindexes in the background.
func (x *indexer) apply(roots, exclude []string) error {
	src, err := localfiles.New(roots, exclude)
	if err != nil {
		return err
	}
	x.run(src)
	return nil
}

func (x *indexer) run(src *localfiles.Source) {
	x.mu.Lock()
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
		if errors.Is(err, filesearch.ErrSaveFailed) {
			log.Printf("files: %v", err) // индекс подменен и работает, не записан только снимок
			err = nil
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		switch {
		case err != nil:
			log.Printf("files: %v", err)
			if src != x.src {
				_ = src.Close()
			}
			if gen == x.gen {
				x.status = indexStatus{Indexed: x.status.Indexed, Skipped: x.status.Skipped, Error: err.Error()}
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
				x.status = indexStatus{Indexed: st.Indexed, Skipped: st.Skipped, Reused: st.Reused}
			}
		}
	}()
}

// Status returns the state of the latest build.
func (x *indexer) Status() indexStatus {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.status
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
