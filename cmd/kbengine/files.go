package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/fileindexgob"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

func init() { commands["files"] = withoutStdin(runFiles) }

// runFiles ищет по файлам в папках на этой машине — первая ступень
// приложения поиска (kb-desktop): тот же поиск потом позовут MCP и окно.
//
// ponytail: индекс строится заново на каждый запуск и живет в памяти. Потолок:
// десятки тысяч файлов (замер 03.11: 3059 файлов рабочей папки за 2,2–2,7 с).
// Путь: хранить
// индекс на диске и обновлять по времени изменения, когда добавится ~/git.
func runFiles(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var roots, exclude repeatedPath
	fs.Var(&roots, "root", "папка для поиска (можно несколько раз)")
	fs.Var(&exclude, "exclude", "имя папки, в которую не заходить (можно несколько раз)")
	q := fs.String("q", "", "поисковый запрос")
	limit := fs.Int("limit", 10, "сколько файлов показать")
	indexPath := fs.String("index", "", "файл снимка индекса: со второго запуска перечитываются только измененные файлы")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if len(roots) == 0 || *q == "" {
		fmt.Fprintln(stderr, "files: нужны --root и --q")
		return 2
	}
	for i, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			fmt.Fprintf(stderr, "files: %v\n", err)
			return 1
		}
		roots[i] = abs
	}

	src, err := localfiles.New(roots, exclude)
	if err != nil {
		fmt.Fprintf(stderr, "files: %v\n", err)
		return 1
	}
	var opts []filesearch.Option
	if *indexPath != "" {
		opts = append(opts, filesearch.WithStore(fileindexgob.Store{Path: *indexPath}))
	}
	svc := filesearch.NewService(src, opts...)
	start := time.Now()
	st, err := svc.Build(context.Background())
	if errors.Is(err, filesearch.ErrSaveFailed) {
		fmt.Fprintf(stderr, "files: %v\n", err) // поиск работает, снимок не записан
	} else if err != nil {
		fmt.Fprintf(stderr, "files: %v\n", err)
		return 1
	}
	hits, err := svc.Search(*q, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "files: %v\n", err)
		return 1
	}

	// Сколько проиндексировано и сколько не прочиталось — до выдачи: «не
	// нашлось» без размера индекса не отличить от «не искали».
	fmt.Fprintf(stdout, "indexed %d files (skipped %d, reused %d) in %s\n", st.Indexed, st.Skipped, st.Reused, time.Since(start).Round(time.Millisecond))
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "ничего не найдено")
		return 0
	}
	for _, h := range hits {
		fmt.Fprintf(stdout, "\n%s\n  %s\n", h.Doc.Path(), h.Snippet)
	}
	return 0
}
