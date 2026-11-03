package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/fileindexgob"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// filesRebuildEvery — как часто индекс догоняет диск. Сборка инкрементальная:
// неизмененные файлы берутся из прошлого индекса без чтения (замер #374: 1,3 с
// на 40 тыс. файлов), так что цена — обход каталогов, а не чтение.
const filesRebuildEvery = 10 * time.Minute

// newFileSearch builds the file search over the folders chosen in the
// kb-desktop window. Индекс не собирается здесь: сервер отвечает агенту сразу,
// а search_files до конца сборки говорит «еще собирается».
func newFileSearch(cfgPath, home string, stderr io.Writer) (*filesearch.Service, error) {
	cfg, err := desktopconfig.Read(cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("конфига %s нет: папки для поиска выбираются в окне kb-desktop, запустите его один раз", cfgPath)
	}
	if err != nil {
		return nil, err
	}
	roots, skipped := desktopconfig.NormalizeRoots(cfg.Roots, home)
	for _, s := range skipped {
		fmt.Fprintf(stderr, "kbengine-mcp: папка пропущена: %s\n", s)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("в %s нет ни одной доступной папки для поиска", cfgPath)
	}
	src, err := localfiles.New(roots, cfg.Exclude)
	if err != nil {
		return nil, err
	}
	return filesearch.NewService(src, fileindexgob.Option(desktopconfig.IndexPath(cfgPath))), nil
}

// keepIndexed builds the index now and then every filesRebuildEvery until ctx
// ends. Ошибка сборки не роняет сервер: каталог отвечает по-прежнему, а
// search_files называет причину сам.
//
// ponytail: пересборка по часам, а не по событиям ФС. Потолок: файл, сохраненный
// минуту назад, не находится до 10 минут. Путь: FSEvents (cgo) или проверка
// mtime корней перед поиском.
func keepIndexed(ctx context.Context, svc *filesearch.Service, stderr io.Writer) {
	build := func() {
		start := time.Now()
		st, err := svc.Build(ctx)
		if st.Warning != "" {
			fmt.Fprintf(stderr, "kbengine-mcp: %s\n", st.Warning)
		}
		if err != nil {
			fmt.Fprintf(stderr, "kbengine-mcp: индекс файлов: %v\n", err)
			return
		}
		fmt.Fprintf(stderr, "kbengine-mcp: файлов в индексе %d (не прочитано %d, из снимка %d) за %s\n",
			st.Indexed, st.Skipped, st.Reused, time.Since(start).Round(time.Millisecond))
	}
	build()
	t := time.NewTicker(filesRebuildEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			build()
		}
	}
}

func userHome() string {
	home, _ := os.UserHomeDir() // без домашней папки «~» в конфиге просто не раскроется
	return home
}
