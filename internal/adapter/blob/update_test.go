package blob_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

// Update — общий цикл «прочитать, изменить, записать под прочитанной версией,
// при конфликте повторить с заново прочитанным»: им пишут и каталог, и журнал
// финансов.
func TestUpdateConcurrentChangesAllLand(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/u1/fin/journal.jsonl", nil, ""); err != nil {
		t.Fatal(err)
	}
	const n = 6
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			err := blob.Update(ctx, s, "users/u1/fin/journal.jsonl", func(raw []byte) ([]byte, error) {
				return append(raw, fmt.Appendf(nil, "line%d\n", i)...), nil
			})
			if err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	o, err := s.Get(ctx, "users/u1/fin/journal.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if !bytes.Contains(o.Data, fmt.Appendf(nil, "line%d\n", i)) {
			t.Errorf("change %d lost: %q", i, o.Data)
		}
	}
}

func TestUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := blob.Update(ctx, s, "k", func(raw []byte) ([]byte, error) { return raw, nil }); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing key: %v, want ErrNotFound", err)
	}
	if _, err := s.Put(ctx, "k", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("change refused")
	if err := blob.Update(ctx, s, "k", func([]byte) ([]byte, error) { return nil, stop }); !errors.Is(err, stop) {
		t.Fatalf("refused change: %v", err)
	}
	if o, _ := s.Get(ctx, "k"); string(o.Data) != "v1" {
		t.Fatalf("a refused change wrote: %q", o.Data)
	}
}
