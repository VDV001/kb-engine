// Package blobtest is the contract every blob.Store must keep. Диск и бакет
// проверяются одними и теми же случаями: иначе адаптеры разошлись бы молча, и
// то, что прошло на Маке, теряло бы запись в облаке.
package blobtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

// Run checks the contract on stores made by newStore. Каждый случай получает
// свой чистый Store, поэтому порядок случаев значения не имеет.
func Run(t *testing.T, newStore func(t *testing.T) blob.Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("get missing key is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Get(ctx, "users/u1/kb/catalog.json"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Get missing: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("create then read back", func(t *testing.T) {
		s := newStore(t)
		etag, err := s.Put(ctx, "users/u1/kb/catalog.json", []byte(`{"entries":[]}`), "")
		if err != nil {
			t.Fatalf("Put create: %v", err)
		}
		if etag == "" {
			t.Fatal("Put create returned an empty etag")
		}
		got, err := s.Get(ctx, "users/u1/kb/catalog.json")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if string(got.Data) != `{"entries":[]}` || got.ETag != etag {
			t.Fatalf("Get = %q etag %q, want the written data and etag %q", got.Data, got.ETag, etag)
		}
	})

	t.Run("create over an existing key is ErrConflict and keeps the data", func(t *testing.T) {
		s := newStore(t)
		mustPut(t, s, "k", "first", "")
		if _, err := s.Put(ctx, "k", []byte("second"), ""); !errors.Is(err, blob.ErrConflict) {
			t.Fatalf("second create: err = %v, want ErrConflict", err)
		}
		mustRead(t, s, "k", "first")
	})

	t.Run("update with the current etag replaces the data", func(t *testing.T) {
		s := newStore(t)
		etag := mustPut(t, s, "k", "v1", "")
		next, err := s.Put(ctx, "k", []byte("v2"), etag)
		if err != nil {
			t.Fatalf("Put with current etag: %v", err)
		}
		if next == etag {
			t.Fatalf("etag did not change after the data changed: %q", next)
		}
		mustRead(t, s, "k", "v2")
	})

	t.Run("update with a stale etag is ErrConflict and keeps the newer data", func(t *testing.T) {
		s := newStore(t)
		stale := mustPut(t, s, "k", "v1", "")
		mustPut(t, s, "k", "v2", stale)
		if _, err := s.Put(ctx, "k", []byte("v3"), stale); !errors.Is(err, blob.ErrConflict) {
			t.Fatalf("Put with stale etag: err = %v, want ErrConflict", err)
		}
		mustRead(t, s, "k", "v2")
	})

	t.Run("update of a key that is gone is ErrConflict", func(t *testing.T) {
		// Писатель думал, что ключ есть: для него это чужое изменение, а не
		// «нет такого», и создавать заново молча нельзя.
		s := newStore(t)
		if _, err := s.Put(ctx, "k", []byte("v"), `"0123"`); !errors.Is(err, blob.ErrConflict) {
			t.Fatalf("Put with etag on a missing key: err = %v, want ErrConflict", err)
		}
		if _, err := s.Get(ctx, "k"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("the refused Put created the key: err = %v", err)
		}
	})

	t.Run("empty object and cyrillic key round-trip", func(t *testing.T) {
		s := newStore(t)
		key := "users/u1/kb/notes/2026-11-05_тема заметки.md"
		mustPut(t, s, key, "", "")
		mustRead(t, s, key, "")
	})

	t.Run("of concurrent writers from one version exactly one wins", func(t *testing.T) {
		s := newStore(t)
		base := mustPut(t, s, "k", "v0", "")
		const writers = 8
		var wg sync.WaitGroup
		errs := make([]error, writers)
		for i := range writers {
			wg.Go(func() {
				_, errs[i] = s.Put(ctx, "k", fmt.Appendf(nil, "w%d", i), base)
			})
		}
		wg.Wait()
		won, lost := -1, 0
		for i, err := range errs {
			switch {
			case err == nil:
				if won >= 0 {
					t.Fatalf("writers %d and %d both won from the same version", won, i)
				}
				won = i
			case errors.Is(err, blob.ErrConflict):
				lost++
			default:
				t.Fatalf("writer %d: unexpected error %v", i, err)
			}
		}
		if won < 0 || lost != writers-1 {
			t.Fatalf("won %d, lost %d of %d: want exactly one winner", won, lost, writers)
		}
		mustRead(t, s, "k", fmt.Sprintf("w%d", won))
	})

	t.Run("invalid keys are refused before any I/O", func(t *testing.T) {
		s := newStore(t)
		for _, key := range InvalidKeys {
			if _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("Get(%q): err = %v, want ErrInvalidKey", key, err)
			}
			if _, err := s.Put(ctx, key, []byte("x"), ""); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("Put(%q): err = %v, want ErrInvalidKey", key, err)
			}
		}
	})

	t.Run("object over the size limit is refused", func(t *testing.T) {
		s := newStore(t)
		big := make([]byte, blob.MaxObjectSize+1)
		if _, err := s.Put(ctx, "k", big, ""); !errors.Is(err, blob.ErrTooLarge) {
			t.Fatalf("Put of %d bytes: err = %v, want ErrTooLarge", len(big), err)
		}
	})
}

// InvalidKeys are keys no store may accept. Через них ключ одного пользователя
// дотянулся бы до чужого каталога (`..`) или до корня диска (`/`).
var InvalidKeys = []string{
	"",
	"/abs",
	"trailing/",
	"a//b",
	"..",
	"../escape",
	"users/u1/../u2/fin/journal.jsonl",
	"./a",
	"a/./b",
	`a\b`,
	"a\x00b",
	"a\nb",
	"\xff\xfe",
}

func mustPut(t *testing.T, s blob.Store, key, data, ifMatch string) string {
	t.Helper()
	etag, err := s.Put(context.Background(), key, []byte(data), ifMatch)
	if err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
	return etag
}

func mustRead(t *testing.T, s blob.Store, key, want string) {
	t.Helper()
	got, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got.Data) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got.Data, want)
	}
}
