package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/extractcache"
)

// fakeExtractor стоит на месте kb-extract и считает вызовы по путям.
type fakeExtractor struct {
	mu    sync.Mutex
	calls map[string]int
	text  string
	err   error
}

func (f *fakeExtractor) Extract(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[filepath.Base(path)]++
	return f.text, f.err
}

func (f *fakeExtractor) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// waitSettled ждет конца сборки и прохода распознавания.
func waitSettled(t *testing.T, x *indexer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !x.Status().Building && !x.extracting.Load() {
			time.Sleep(50 * time.Millisecond) // пересборка после прохода могла только начаться
			if !x.Status().Building && !x.extracting.Load() {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("сборка и распознавание не закончились за 10 с")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Окно распознает картинку, которой нет в кеше, и пересобирает индекс: текст
// скана находится поиском.
func TestIndexer_ExtractsPendingAndReindexes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "scan.png"), "png")
	writeFile(t, filepath.Join(root, "note.md"), "заметка")
	ext := &fakeExtractor{text: "СЧЕТ НА ОПЛАТУ"}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "",
		extraction{cache: extractcache.Cache{Dir: t.TempDir()}, helper: ext})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	waitSettled(t, x)
	hits, err := x.files.Search("счет оплату", 5)
	if err != nil || len(hits) != 1 || hits[0].Doc.Name() != "scan.png" {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if n := ext.count("scan.png"); n != 1 {
		t.Fatalf("распознаваний %d, ждали 1", n)
	}
}

// Файл, который помощник не прочитал, не распознается на каждом проходе:
// в кеше остается пустой текст, и файл ищется по имени (ревью #378: битый
// файл гонялся заново на каждой сборке).
func TestIndexer_FailedExtractionIsNotRetried(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "broken.pdf"), "%PDF")
	ext := &fakeExtractor{err: errors.New("boom")}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "",
		extraction{cache: extractcache.Cache{Dir: t.TempDir()}, helper: ext})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	waitSettled(t, x)
	if err := x.apply([]string{root}, nil); err != nil {
		t.Fatal(err)
	}
	waitSettled(t, x)
	if n := ext.count("broken.pdf"); n != 1 {
		t.Fatalf("распознаваний %d, ждали 1", n)
	}
	if hits, _ := x.files.Search("broken", 5); len(hits) != 1 {
		t.Fatalf("файл не ищется по имени: %+v", hits)
	}
}

// Без помощника окно только читает готовый кеш и ничего не распознает.
func TestIndexer_WithoutHelperReadsCache(t *testing.T) {
	root := t.TempDir()
	scan := filepath.Join(root, "scan.png")
	writeFile(t, scan, "png")
	info, err := os.Stat(scan)
	if err != nil {
		t.Fatal(err)
	}
	cache := extractcache.Cache{Dir: t.TempDir()}
	if err := cache.Put(scan, info.Size(), info.ModTime(), "акт сверки"); err != nil {
		t.Fatal(err)
	}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "", extraction{cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	waitSettled(t, x)
	if hits, _ := x.files.Search("сверки", 5); len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
}
