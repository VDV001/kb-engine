package catalogjson_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Чистые функции дают те же байты, что файловые: один путь записи для диска
// и хранилища.
func TestDocFunctionsMatchTheFileWriters(t *testing.T) {
	src := []byte(`{"last_updated":"2026-11-05","entries":[{"id":1,"habr_id":1,"title":"A","url":"https://h/1","category":"golang","status":"keep","x_unknown":true}],"meta":{"categories":{"golang":"Go"}}}`)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	ch := catalogjson.Changes{Verdict: "consider", AddTags: []string{"go"}}
	if _, err := catalogjson.SetFields(path, []int{1}, ch); err != nil {
		t.Fatal(err)
	}
	fromFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fromDoc, n, err := catalogjson.SetFieldsDoc(src, []int{1}, ch)
	if err != nil || n != 1 {
		t.Fatalf("SetFieldsDoc: %d, %v", n, err)
	}
	if !bytes.Equal(fromFile, fromDoc) {
		t.Fatalf("SetFieldsDoc differs from SetFields:\nfile %s\ndoc  %s", fromFile, fromDoc)
	}
	if !bytes.Contains(fromDoc, []byte(`"x_unknown": true`)) || !bytes.Contains(fromDoc, []byte(`"last_updated"`)) {
		t.Fatalf("fields the domain does not know were lost: %s", fromDoc)
	}
}

func TestStoreWriterUpdatesUnderTheVersionItRead(t *testing.T) {
	ctx := context.Background()
	s := newCountingStore(t)
	if _, err := s.Put(ctx, catalogKey, []byte(catalogOf("A")), ""); err != nil {
		t.Fatal(err)
	}
	w := catalogjson.NewStoreWriter(s, catalogKey)
	err := w.Update(ctx, func(raw []byte) ([]byte, error) {
		doc, _, err := catalogjson.SetFieldsDoc(raw, []int{1}, catalogjson.Changes{Verdict: "consider"})
		return doc, err
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	o, err := s.Get(ctx, catalogKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(o.Data, []byte(`"consider"`)) {
		t.Fatalf("catalog after Update: %s", o.Data)
	}
}

// Одновременные правки не теряются: конфликт — перечитать и применить заново.
func TestStoreWriterConcurrentUpdatesAllLand(t *testing.T) {
	ctx := context.Background()
	s := newCountingStore(t)
	if _, err := s.Put(ctx, catalogKey, []byte(`{"entries":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	w := catalogjson.NewStoreWriter(s, catalogKey)
	const n = 6
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			err := w.Update(ctx, func(raw []byte) ([]byte, error) {
				return append(bytes.TrimSuffix(bytes.TrimSpace(raw), []byte("}")), fmt.Appendf(nil, `,"m%d":1}`, i)...), nil
			})
			if err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	o, err := s.Get(ctx, catalogKey)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if !bytes.Contains(o.Data, fmt.Appendf(nil, `"m%d":1`, i)) {
			t.Errorf("update %d lost: %s", i, o.Data)
		}
	}
}

func TestStoreWriterRefusals(t *testing.T) {
	ctx := context.Background()
	s := newCountingStore(t)
	w := catalogjson.NewStoreWriter(s, catalogKey)
	if err := w.Update(ctx, func(raw []byte) ([]byte, error) { return raw, nil }); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Update of a missing catalog: %v, want ErrNotFound", err)
	}
	if _, err := s.Put(ctx, catalogKey, []byte(catalogOf("A")), ""); err != nil {
		t.Fatal(err)
	}
	refuse := errors.New("refused by the change")
	if err := w.Update(ctx, func([]byte) ([]byte, error) { return nil, refuse }); !errors.Is(err, refuse) {
		t.Fatalf("Update: %v, want the change's error", err)
	}
	o, _ := s.Get(ctx, catalogKey)
	if !bytes.Equal(o.Data, []byte(catalogOf("A"))) {
		t.Fatal("a refused change wrote anyway")
	}
}
