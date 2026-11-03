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
	_, x, err := newAPI(config{Roots: []string{a}})
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
	_, x, err := newAPI(config{Roots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := x.apply([]string{"relative"}, nil); err == nil {
		t.Fatal("относительный корень обязан отказать")
	}
}
