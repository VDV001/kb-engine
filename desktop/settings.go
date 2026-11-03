package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// foldersView is what the settings panel shows and sends back.
type foldersView struct {
	Roots   []string `json:"roots"`
	Exclude []string `json:"exclude"`
}

// folderSettings — какие папки индексируются. Меняется только из окна
// (привязки Wails), а не через HTTP API: иначе любой, кто достучался до API,
// мог бы направить индекс на чужие папки.
type folderSettings struct {
	mu    sync.Mutex
	path  string // config.json
	home  string
	cfg   config
	apply func(roots, exclude []string) error // запускает переиндексацию
}

// Get returns the current folders.
func (f *folderSettings) Get() foldersView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return foldersView{Roots: slices.Clone(f.cfg.Roots), Exclude: slices.Clone(f.cfg.Exclude)}
}

// Set validates the folders, saves them and starts reindexing.
//
// Проверка всех папок — до записи: одна опечатка в списке отклоняет весь
// список с ее названием, ничего не меняя. Молча выкинуть плохую папку и
// сохранить остальные значило бы, что человек думает, будто ищет там, где
// поиска нет.
func (f *folderSettings) Set(roots, exclude []string) error {
	clean, skipped := normalizeRoots(roots, f.home)
	if len(skipped) > 0 {
		return fmt.Errorf("не подходит: %s", strings.Join(skipped, "; "))
	}
	clean = dedupe(clean)
	if len(clean) == 0 {
		return errors.New("нужна хотя бы одна папка")
	}
	ex := make([]string, 0, len(exclude))
	for _, e := range exclude {
		if e = strings.TrimSpace(e); e != "" && !slices.Contains(ex, e) {
			ex = append(ex, e)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Roots, next.Exclude = clean, ex
	if err := writeConfig(f.path, next); err != nil {
		return fmt.Errorf("сохранить настройки: %w", err)
	}
	f.cfg = next
	return f.apply(clean, ex)
}

func dedupe(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
