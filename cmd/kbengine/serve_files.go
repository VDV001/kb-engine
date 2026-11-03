package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// serveOptions собирает необязательные части сервера из флагов serve.
func serveOptions(ctx context.Context, catalogPath string, timing bool, filesRoots, filesExclude []string, stdout, stderr io.Writer) ([]httpapi.Option, error) {
	opts := []httpapi.Option{synonymsFor(catalogPath, stderr), toolCallsOption(stderr)}
	if timing {
		opts = append(opts, httpapi.WithServerTiming())
	}
	if len(filesRoots) > 0 {
		opt, built := filesOption(ctx, filesRoots, filesExclude, stdout)
		if opt == nil {
			return nil, fmt.Errorf("files: %w", <-built)
		}
		opts = append(opts, opt)
	}
	return opts, nil
}

// filesOption подключает к серверу поиск по файлам в roots и запускает сборку
// индекса в фоне. Канал отдает итог сборки один раз.
//
// Фоном, а не до старта: витрина каталога не должна ждать обхода диска, а
// поиск по файлам до конца сборки честно отвечает 503.
func filesOption(ctx context.Context, roots, exclude []string, log io.Writer) (httpapi.Option, <-chan error) {
	built := make(chan error, 1)
	abs := make([]string, 0, len(roots))
	for _, r := range roots {
		a, err := filepath.Abs(r)
		if err != nil {
			built <- err
			return nil, built
		}
		abs = append(abs, a)
	}
	src, err := localfiles.New(abs, exclude)
	if err != nil {
		built <- err
		return nil, built
	}
	svc := filesearch.NewService(src)
	go func() {
		start := time.Now()
		st, err := svc.Build(ctx)
		if err != nil {
			fmt.Fprintf(log, "files: %v\n", err)
		} else {
			fmt.Fprintf(log, "files: indexed %d files (skipped %d) in %s\n", st.Indexed, st.Skipped, time.Since(start).Round(time.Millisecond))
		}
		built <- err
	}()
	return httpapi.WithFiles(svc), built
}
