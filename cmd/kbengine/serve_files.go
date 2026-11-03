package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// serveOptions собирает необязательные части сервера из флагов serve.
func serveOptions(ctx context.Context, catalogPath string, timing bool, filesRoots, filesExclude []string, filesIndex string, stdout, stderr io.Writer) ([]httpapi.Option, error) {
	opts := []httpapi.Option{synonymsFor(catalogPath, stderr), toolCallsOption(stderr)}
	if timing {
		opts = append(opts, httpapi.WithServerTiming())
	}
	if len(filesRoots) > 0 {
		opt, built := filesOption(ctx, filesRoots, filesExclude, filesIndex, stdout)
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
func filesOption(ctx context.Context, roots, exclude []string, indexPath string, log io.Writer) (httpapi.Option, <-chan error) {
	built := make(chan error, 1)
	svc, err := newFileService(roots, exclude, indexPath)
	if err != nil {
		built <- err
		return nil, built
	}
	go func() {
		start := time.Now()
		st, err := svc.Build(ctx)
		if st.Warning != "" {
			fmt.Fprintf(log, "files: %s\n", st.Warning)
		}
		if errors.Is(err, filesearch.ErrSaveFailed) {
			fmt.Fprintf(log, "files: %v\n", err) // поиск работает, снимок не записан
			err = nil
		}
		if err != nil {
			fmt.Fprintf(log, "files: %v\n", err)
		} else {
			fmt.Fprintf(log, "files: indexed %d files (skipped %d, reused %d) in %s\n", st.Indexed, st.Skipped, st.Reused, time.Since(start).Round(time.Millisecond))
		}
		built <- err
	}()
	return httpapi.WithFiles(svc), built
}
