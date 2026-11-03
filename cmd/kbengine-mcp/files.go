package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/extractcache"
	"github.com/daniil/kb-engine/internal/adapter/fileindexgob"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

const (
	// configPollEvery — как часто сверяется config.json окна. Папка, закрытая
	// в окне, закрывается для агента не позже чем через столько плюс сборку:
	// это граница чтения, и ждать ее десять минут нельзя (ревью #375).
	configPollEvery = 30 * time.Second
	// filesRebuildEvery — как часто индекс догоняет диск. Сборка
	// инкрементальная: неизмененные файлы берутся из прошлого индекса без
	// чтения (замер #374: 1,3 с на 40 тыс. файлов).
	filesRebuildEvery = 10 * time.Minute
)

// fileIndex keeps the file search in step with the kb-desktop window: the
// folders come from its config.json, re-read whenever the window saves it.
//
// Поля, кроме svc, трогает только одна горутина (run); svc сам безопасен для
// поиска во время пересборки.
//
// ponytail: каждая сессия Claude Code поднимает свой kbengine-mcp со своим
// индексом в памяти и своим обходом. Потолок: несколько сессий на ~/git
// (~1 ГБ индекса каждая). Путь: индекс держит один процесс (окно или serve),
// а MCP ходит к нему по /api/files/*.
type fileIndex struct {
	svc     *filesearch.Service
	cfgPath string
	home    string
	stderr  io.Writer
	stamp   time.Time          // mtime конфига, по которому открыт src
	src     *localfiles.Source // открытые корни текущего индекса
	builtAt time.Time          // нулевой — удачной сборки еще не было
}

// newFileIndex opens the folders named in the window's config. Индекс здесь не
// собирается: сервер отвечает агенту сразу, а search_files до конца сборки
// говорит «еще собирается».
func newFileIndex(cfgPath, home string, stderr io.Writer) (*fileIndex, error) {
	x := &fileIndex{cfgPath: expandHome(cfgPath, home), home: home, stderr: stderr}
	src, stamp, err := x.open()
	if err != nil {
		return nil, err
	}
	x.src, x.stamp = src, stamp
	x.svc = filesearch.NewService(src, fileindexgob.Option(desktopconfig.IndexPath(x.cfgPath)))
	return x, nil
}

// open reads the config and opens its folders.
func (x *fileIndex) open() (*localfiles.Source, time.Time, error) {
	info, err := os.Stat(x.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, fmt.Errorf("конфига %s нет: папки для поиска выбираются в окне kb-desktop, запустите его один раз", x.cfgPath)
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	cfg, err := desktopconfig.Read(x.cfgPath)
	if err != nil {
		return nil, time.Time{}, err
	}
	roots, skipped := desktopconfig.NormalizeRoots(cfg.Roots, x.home)
	for _, s := range skipped {
		fmt.Fprintf(x.stderr, "kbengine-mcp: папка пропущена: %s\n", s)
	}
	if len(roots) == 0 {
		return nil, time.Time{}, fmt.Errorf("в %s нет ни одной доступной папки для поиска", x.cfgPath)
	}
	// Текст PDF и картинок распознает окно; MCP только читает его кеш.
	src, err := localfiles.New(roots, cfg.Exclude,
		localfiles.WithExtracted(extractcache.Cache{Dir: desktopconfig.ExtractDir(x.cfgPath)}))
	return src, info.ModTime(), err
}

// run builds the index now, then polls the config and rebuilds on schedule
// until ctx ends.
func (x *fileIndex) run(ctx context.Context) {
	x.refresh(ctx, time.Now())
	t := time.NewTicker(configPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			x.refresh(ctx, now)
		}
	}
}

// refresh switches to the folders of a config the window has saved since,
// otherwise rebuilds when the index is due or the last build failed.
func (x *fileIndex) refresh(ctx context.Context, now time.Time) {
	if info, err := os.Stat(x.cfgPath); err == nil && !info.ModTime().Equal(x.stamp) {
		x.reopen(ctx, now)
		return
	}
	if !x.builtAt.IsZero() && now.Sub(x.builtAt) < filesRebuildEvery {
		return
	}
	start := time.Now()
	st, err := x.svc.Build(ctx)
	if x.report(st, err, start) {
		x.builtAt = now
	}
}

// reopen rebuilds over the folders of the changed config. Пока сборка идет,
// отвечает прежний индекс; при ошибке он и остается, а stamp не двигается —
// следующий опрос попробует снова (окно пишет конфиг не атомарно, и его можно
// застать наполовину записанным).
//
// ponytail: при упавшей сборке закрытая в окне папка остается доступной до
// удачной. Потолок: обход, падающий раз за разом. Путь: при ошибке переходить
// на пустой источник, а не держать прежний.
func (x *fileIndex) reopen(ctx context.Context, now time.Time) {
	src, stamp, err := x.open()
	if err != nil {
		fmt.Fprintf(x.stderr, "kbengine-mcp: конфиг окна изменился, но не применен: %v\n", err)
		return
	}
	start := time.Now()
	st, err := x.svc.Replace(ctx, src)
	if !x.report(st, err, start) {
		_ = src.Close()
		return
	}
	old := x.src
	x.src, x.stamp, x.builtAt = src, stamp, now
	// Не сразу: поиск и чтение, взявшие старый источник до подмены, должны
	// успеть дочитать — тот же прием, что у окна (desktop/indexer.go).
	time.AfterFunc(time.Minute, func() { _ = old.Close() })
}

// report prints what a build did and tells whether the index was swapped in.
func (x *fileIndex) report(st filesearch.Stats, err error, start time.Time) bool {
	if st.Warning != "" {
		fmt.Fprintf(x.stderr, "kbengine-mcp: %s\n", st.Warning)
	}
	if err != nil && !errors.Is(err, filesearch.ErrSaveFailed) {
		fmt.Fprintf(x.stderr, "kbengine-mcp: индекс файлов: %v\n", err)
		return false
	}
	if err != nil {
		fmt.Fprintf(x.stderr, "kbengine-mcp: %v\n", err) // индекс подменен, не записан только снимок
	}
	fmt.Fprintf(x.stderr, "kbengine-mcp: файлов в индексе %d (не прочитано %d, из снимка %d) за %s\n",
		st.Indexed, st.Skipped, st.Reused, time.Since(start).Round(time.Millisecond))
	return true
}

// expandHome раскрывает «~/» в пути: args MCP-конфига приходят из JSON, и shell
// их не раскрывает.
func expandHome(path, home string) string {
	if home != "" && (path == "~" || strings.HasPrefix(path, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}

func userHome() string {
	home, _ := os.UserHomeDir() // без домашней папки «~» просто не раскроется
	return home
}
