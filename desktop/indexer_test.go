package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func waitIdle(t *testing.T, x *indexer) indexStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := x.Status(); !st.Building {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("индексация не закончилась за 10 с")
	return indexStatus{}
}

func TestIndexer_ApplyReindexesNewFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(a, "old.md"), []byte("договор"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "new.md"), []byte("договор поставки"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{a}}, "", extraction{})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	if st := waitIdle(t, x); st.Indexed != 1 || st.Error != "" {
		t.Fatalf("первая сборка: %+v", st)
	}

	if err := x.apply([]string{b}, nil); err != nil {
		t.Fatal(err)
	}
	if st := waitIdle(t, x); st.Indexed != 1 || st.Error != "" {
		t.Fatalf("после смены папок: %+v", st)
	}
	hits, err := x.files.Search("договор", 5)
	if err != nil || len(hits) != 1 || hits[0].Doc.Name() != "new.md" {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
}

func TestIndexer_ApplyRefusesBadRoot(t *testing.T) {
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{t.TempDir()}}, "", extraction{})
	if err != nil {
		t.Fatal(err)
	}
	if err := x.apply([]string{"relative"}, nil); err == nil {
		t.Fatal("относительный корень обязан отказать")
	}
}

func TestIndexer_KeepsIndexOnDisk(t *testing.T) {
	// Окно хранит индекс рядом с config.json: второй запуск берет неизменные
	// файлы из снимка и не перечитывает их (замер 03.11: 28,7 с -> 2,0 с).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("договор"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Файл старше прошлой сборки: свежий перечитывался бы по правилу racy-clean.
	hourAgo := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a.md"), hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(t.TempDir(), "index.gob")
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, index, extraction{})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	waitIdle(t, x)

	_, x2, err := newAPI(desktopconfig.Config{Roots: []string{root}}, index, extraction{})
	if err != nil {
		t.Fatal(err)
	}
	x2.start(context.Background())
	if st := waitIdle(t, x2); st.Reused != 1 || st.Indexed != 1 {
		t.Fatalf("второй запуск: %+v", st)
	}
}

// Окно догоняет диск по таймеру, как MCP: файл, сохраненный после запуска,
// находится без перезапуска окна (прежде индекс собирался только при старте
// и смене папок).
func TestIndexer_RebuildsOnSchedule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.md"), []byte("старое"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "", extraction{})
	if err != nil {
		t.Fatal(err)
	}
	x.every = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x.start(ctx)
	waitIdle(t, x)
	if err := os.WriteFile(filepath.Join(root, "new.md"), []byte("свежая мысль"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hits, _ := x.files.Search("свежая", 5); len(hits) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("новый файл не нашелся: окно не пересобирает индекс")
}
