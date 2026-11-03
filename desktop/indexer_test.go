package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	_, x, err := newAPI(config{Roots: []string{a}}, "")
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
	_, x, err := newAPI(config{Roots: []string{t.TempDir()}}, "")
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
	index := filepath.Join(t.TempDir(), "index.gob")
	_, x, err := newAPI(config{Roots: []string{root}}, index)
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	waitIdle(t, x)

	_, x2, err := newAPI(config{Roots: []string{root}}, index)
	if err != nil {
		t.Fatal(err)
	}
	x2.start(context.Background())
	if st := waitIdle(t, x2); st.Reused != 1 || st.Indexed != 1 {
		t.Fatalf("второй запуск: %+v", st)
	}
}
