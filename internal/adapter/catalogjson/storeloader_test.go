package catalogjson_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

const catalogKey = "users/u1/kb/_data/catalog.json"

func catalogOf(titles ...string) string {
	entries := make([]string, len(titles))
	for i, title := range titles {
		entries[i] = fmt.Sprintf(`{"id":%d,"habr_id":%d,"title":%q,"url":"https://h/","category":"golang","status":"read"}`, i+1, i+1, title)
	}
	return `{"entries":[` + strings.Join(entries, ",") + `]}`
}

// countingStore counts full reads: условный ответ «не изменился» полным
// чтением не считается.
type countingStore struct {
	blob.Store
	full atomic.Int64
}

func (c *countingStore) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	o, err := c.Store.GetIfChanged(ctx, key, etag)
	if err == nil {
		c.full.Add(1)
	}
	return o, err
}

func newCountingStore(t *testing.T) *countingStore {
	t.Helper()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &countingStore{Store: s}
}

func TestStoreLoaderReadsOnceWhileUnchanged(t *testing.T) {
	s := newCountingStore(t)
	if _, err := s.Put(context.Background(), catalogKey, []byte(catalogOf("A", "B")), ""); err != nil {
		t.Fatal(err)
	}
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	first, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if first.Len() != 2 {
		t.Fatalf("Len = %d, want 2", first.Len())
	}
	for range 5 {
		again, err := l.Load()
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatal("unchanged catalog was decoded again")
		}
	}
	if n := s.full.Load(); n != 1 {
		t.Fatalf("%d full reads of an unchanged catalog, want 1", n)
	}
}

func TestStoreLoaderSeesANewVersion(t *testing.T) {
	s := newCountingStore(t)
	etag, err := s.Put(context.Background(), catalogKey, []byte(catalogOf("A")), "")
	if err != nil {
		t.Fatal(err)
	}
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	if c, err := l.Load(); err != nil || c.Len() != 1 {
		t.Fatalf("first Load: %v", err)
	}
	if _, err := s.Put(context.Background(), catalogKey, []byte(catalogOf("A", "B", "C")), etag); err != nil {
		t.Fatal(err)
	}
	c, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Len() != 3 {
		t.Fatalf("after an update Len = %d, want 3", c.Len())
	}
}

func TestStoreLoaderMissingCatalogIsAnError(t *testing.T) {
	s := newCountingStore(t)
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	if _, err := l.Load(); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Load of a missing catalog: err = %v, want ErrNotFound", err)
	}
}

// Битая новая версия — ошибка, а не тихий показ прежней: иначе правка,
// сломавшая каталог, выглядела бы как правка, которая не дошла.
func TestStoreLoaderBrokenNewVersionIsAnError(t *testing.T) {
	s := newCountingStore(t)
	etag, err := s.Put(context.Background(), catalogKey, []byte(catalogOf("A")), "")
	if err != nil {
		t.Fatal(err)
	}
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	if _, err := l.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), catalogKey, []byte(`{"entries":[`), etag); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(); err == nil {
		t.Fatal("Load of a broken catalog returned the old one without an error")
	}
}

// Один экземпляр каталога отдается всем запросам сразу.
func TestStoreLoaderConcurrentLoads(t *testing.T) {
	s := newCountingStore(t)
	if _, err := s.Put(context.Background(), catalogKey, []byte(catalogOf("A", "B")), ""); err != nil {
		t.Fatal(err)
	}
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			c, err := l.Load()
			if err != nil || c.Len() != 2 {
				t.Errorf("Load: %v", err)
				return
			}
			_ = c.Entries()
		})
	}
	wg.Wait()
}

// flakyStore fails GetIfChanged with err while it is set.
type flakyStore struct {
	blob.Store
	err atomic.Pointer[error]
}

func (f *flakyStore) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	if e := f.err.Load(); e != nil {
		return blob.Object{}, *e
	}
	return f.Store.GetIfChanged(ctx, key, etag)
}

// Сбой бакета (сеть, 5xx) не роняет витрину, пока есть разобранный каталог;
// пропавший каталог — ошибка: это не сбой связи, а факт о данных.
func TestStoreLoaderServesTheCachedCatalogOnATransientError(t *testing.T) {
	base := newCountingStore(t)
	if _, err := base.Put(context.Background(), catalogKey, []byte(catalogOf("A")), ""); err != nil {
		t.Fatal(err)
	}
	s := &flakyStore{Store: base}
	l := catalogjson.NewStoreLoader(context.Background(), s, catalogKey)
	first, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	transient := errors.New("blobs3: get: status 503 ServiceUnavailable")
	s.err.Store(&transient)
	got, err := l.Load()
	if err != nil || got != first {
		t.Fatalf("Load during a 503 = %v, %v; want the cached catalog", got, err)
	}
	gone := fmt.Errorf("%w: %s (404)", blob.ErrNotFound, catalogKey)
	s.err.Store(&gone)
	if _, err := l.Load(); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Load after the catalog is gone: err = %v, want ErrNotFound", err)
	}
}

// Пока условный GET идет, остальные запросы ждут его результат, а не
// становятся в очередь со своими: шесть вызовов страницы — один GET.
func TestStoreLoaderSharesOneInFlightRead(t *testing.T) {
	base := newCountingStore(t)
	if _, err := base.Put(context.Background(), catalogKey, []byte(catalogOf("A")), ""); err != nil {
		t.Fatal(err)
	}
	g := &gatedStore{Store: base, gate: make(chan struct{})}
	l := catalogjson.NewStoreLoader(context.Background(), g, catalogKey)
	const n = 16
	var entered atomic.Int64
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			entered.Add(1)
			if c, err := l.Load(); err != nil || c.Len() != 1 {
				t.Errorf("Load: %v", err)
			}
		})
	}
	for entered.Load() < n {
		runtime.Gosched()
	}
	time.Sleep(50 * time.Millisecond) // все вошли в Load и ждут
	close(g.gate)
	wg.Wait()
	if c := g.calls.Load(); c != 1 {
		t.Fatalf("%d reads for %d concurrent loads, want 1", c, n)
	}
}

// gatedStore holds every GetIfChanged until gate is closed and counts calls.
type gatedStore struct {
	blob.Store
	gate  chan struct{}
	calls atomic.Int64
}

func (g *gatedStore) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	g.calls.Add(1)
	<-g.gate
	return g.Store.GetIfChanged(ctx, key, etag)
}
