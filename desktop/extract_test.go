package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/extractcache"
)

// fakeExtractor стоит на месте kb-extract: файл узнает по содержимому из
// stdin (путь помощнику больше не передается) и считает вызовы.
type fakeExtractor struct {
	mu    sync.Mutex
	calls map[string]int
	text  string
	err   error
	hold  map[string]chan struct{} // содержимое, на котором ждать закрытия канала
	began chan string              // сообщает о начале распознавания
}

func (f *fakeExtractor) Extract(_ context.Context, r io.Reader, _ string) (string, error) {
	b, _ := io.ReadAll(r)
	body := string(b)
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[body]++
	wait := f.hold[body]
	f.mu.Unlock()
	if f.began != nil {
		f.began <- body
	}
	if wait != nil {
		<-wait
	}
	return f.text + " " + body, f.err
}

func (f *fakeExtractor) count(body string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[body]
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
	writeFile(t, filepath.Join(root, "scan.png"), "scan")
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
	if n := ext.count("scan"); n != 1 {
		t.Fatalf("распознаваний %d, ждали 1", n)
	}
}

// Файл, который помощник не прочитал, не распознается на каждом проходе:
// в кеше остается пустой текст, и файл ищется по имени (ревью #378: битый
// файл гонялся заново на каждой сборке).
func TestIndexer_FailedExtractionIsNotRetried(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "broken.pdf"), "broken")
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
	if n := ext.count("broken"); n != 1 {
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

// Таймаут — не «файл битый»: пустой текст в кеш не кладется, и следующий
// проход распознает файл снова (ревью #379).
func TestIndexer_TimeoutIsRetried(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "slow.pdf"), "slow")
	ext := &fakeExtractor{err: fmt.Errorf("kb-extract: %w", extractcache.ErrTimeout)}
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
	if n := ext.count("slow"); n != 2 {
		t.Fatalf("распознаваний %d, ждали 2: таймаут закешировался", n)
	}
}

// Смена папок посреди прохода: проход по старым папкам не пересобирает индекс
// на них (выбор откатывался), а новые папки получают свой проход (терялся).
func TestIndexer_FolderChangeDuringPass(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(a, "a.png"), "A")
	writeFile(t, filepath.Join(a, "z.png"), "A2") // после a.png в порядке обхода
	writeFile(t, filepath.Join(a, "only-a.md"), "старая папка")
	writeFile(t, filepath.Join(b, "b.png"), "B")
	release := make(chan struct{})
	ext := &fakeExtractor{text: "текст", hold: map[string]chan struct{}{"A": release}, began: make(chan string, 4)}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{a}}, "",
		extraction{cache: extractcache.Cache{Dir: t.TempDir()}, helper: ext})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	if got := <-ext.began; got != "A" {
		t.Fatalf("первым распознается %q", got)
	}
	if err := x.apply([]string{b}, nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, x)
	close(release)
	waitSettled(t, x)
	if n := ext.count("B"); n != 1 {
		t.Fatalf("новые папки распознаны %d раз, ждали 1", n)
	}
	// Остаток старых папок после смены не распознается: проход остановился.
	if n := ext.count("A2"); n != 0 {
		t.Fatalf("старая папка распознавалась дальше: %d", n)
	}
	if hits, _ := x.files.Search("старая папка", 5); len(hits) != 0 {
		t.Fatalf("индекс откатился на старые папки: %+v", hits)
	}
	if hits, _ := x.files.Search("текст", 5); len(hits) != 1 || hits[0].Doc.Name() != "b.png" {
		t.Fatalf("hits = %+v", hits)
	}
}

// Окно показывает, сколько распознано из скольких: первый проход идет
// десятки минут, и без счета он неотличим от зависания.
func TestIndexer_ReportsRecognitionProgress(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.png"), "A")
	writeFile(t, filepath.Join(root, "b.png"), "B")
	release := make(chan struct{})
	ext := &fakeExtractor{text: "текст", hold: map[string]chan struct{}{"B": release}, began: make(chan string, 4)}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "",
		extraction{cache: extractcache.Cache{Dir: t.TempDir()}, helper: ext})
	if err != nil {
		t.Fatal(err)
	}
	x.start(context.Background())
	<-ext.began // a.png
	<-ext.began // b.png держится
	st := x.Status()
	if st.Recognizing == nil || st.Recognizing.Total != 2 || st.Recognizing.Done != 1 {
		t.Fatalf("посреди прохода: %+v", st.Recognizing)
	}
	close(release)
	waitSettled(t, x)
	if st := x.Status(); st.Recognizing != nil {
		t.Fatalf("после прохода: %+v", st.Recognizing)
	}
}
