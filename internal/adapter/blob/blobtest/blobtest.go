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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, newStore(t)) })
	}
}

var cases = []struct {
	name  string
	check func(t *testing.T, s blob.Store)
}{
	{"get missing key is ErrNotFound", missingIsNotFound},
	{"create then read back", createThenRead},
	{"create over an existing key is ErrConflict and keeps the data", createOverExisting},
	{"update with the current etag replaces the data", updateWithCurrentETag},
	{"update with a stale etag is ErrConflict and keeps the newer data", updateWithStaleETag},
	{"update of a key that is gone is ErrConflict", updateOfGoneKey},
	{"empty object and cyrillic key round-trip", emptyAndCyrillicKey},
	{"of concurrent writers from one version exactly one wins", oneConcurrentWinner},
	{"invalid keys are refused before any I/O", invalidKeysRefused},
	{"object over the size limit is refused", tooLargeRefused},
	{"get-if-changed on the same version is ErrNotModified", ifChangedSameVersion},
	{"get-if-changed after an update returns the new version", ifChangedNewVersion},
	{"get-if-changed on a missing key is ErrNotFound", ifChangedMissing},
	{"get-if-changed with an empty etag reads like Get", ifChangedEmptyETag},
	{"get-if-changed refuses invalid keys", ifChangedInvalidKeys},
}

func missingIsNotFound(t *testing.T, s blob.Store) {
	ctx := context.Background()
	if _, err := s.Get(ctx, "users/u1/kb/catalog.json"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get missing: err = %v, want ErrNotFound", err)
	}
}

func createThenRead(t *testing.T, s blob.Store) {
	ctx := context.Background()
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
}

func createOverExisting(t *testing.T, s blob.Store) {
	ctx := context.Background()
	mustPut(t, s, "k", "first", "")
	if _, err := s.Put(ctx, "k", []byte("second"), ""); !errors.Is(err, blob.ErrConflict) {
		t.Fatalf("second create: err = %v, want ErrConflict", err)
	}
	mustRead(t, s, "k", "first")
}

func updateWithCurrentETag(t *testing.T, s blob.Store) {
	ctx := context.Background()
	etag := mustPut(t, s, "k", "v1", "")
	next, err := s.Put(ctx, "k", []byte("v2"), etag)
	if err != nil {
		t.Fatalf("Put with current etag: %v", err)
	}
	if next == etag {
		t.Fatalf("etag did not change after the data changed: %q", next)
	}
	mustRead(t, s, "k", "v2")
}

func updateWithStaleETag(t *testing.T, s blob.Store) {
	ctx := context.Background()
	stale := mustPut(t, s, "k", "v1", "")
	mustPut(t, s, "k", "v2", stale)
	if _, err := s.Put(ctx, "k", []byte("v3"), stale); !errors.Is(err, blob.ErrConflict) {
		t.Fatalf("Put with stale etag: err = %v, want ErrConflict", err)
	}
	mustRead(t, s, "k", "v2")
}

// Писатель думал, что ключ есть: для него это чужое изменение, а не
// «нет такого», и создавать заново молча нельзя.
func updateOfGoneKey(t *testing.T, s blob.Store) {
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", []byte("v"), `"0123"`); !errors.Is(err, blob.ErrConflict) {
		t.Fatalf("Put with etag on a missing key: err = %v, want ErrConflict", err)
	}
	if _, err := s.Get(ctx, "k"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("the refused Put created the key: err = %v", err)
	}
}

func emptyAndCyrillicKey(t *testing.T, s blob.Store) {
	key := "users/u1/kb/notes/2026-11-05_тема заметки.md"
	mustPut(t, s, key, "", "")
	mustRead(t, s, key, "")
}

func oneConcurrentWinner(t *testing.T, s blob.Store) {
	ctx := context.Background()
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
}

func invalidKeysRefused(t *testing.T, s blob.Store) {
	ctx := context.Background()
	for _, key := range InvalidKeys {
		if _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Get(%q): err = %v, want ErrInvalidKey", key, err)
		}
		if _, err := s.Put(ctx, key, []byte("x"), ""); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Put(%q): err = %v, want ErrInvalidKey", key, err)
		}
	}
}

func tooLargeRefused(t *testing.T, s blob.Store) {
	ctx := context.Background()
	big := make([]byte, blob.MaxObjectSize+1)
	if _, err := s.Put(ctx, "k", big, ""); !errors.Is(err, blob.ErrTooLarge) {
		t.Fatalf("Put of %d bytes: err = %v, want ErrTooLarge", len(big), err)
	}
}

// Условное чтение — то, на чем держится кэш каталога: неизмененный объект
// не перекачивается.
func ifChangedSameVersion(t *testing.T, s blob.Store) {
	etag := mustPut(t, s, "k", "v1", "")
	if _, err := s.GetIfChanged(context.Background(), "k", etag); !errors.Is(err, blob.ErrNotModified) {
		t.Fatalf("GetIfChanged with the current etag: err = %v, want ErrNotModified", err)
	}
}

func ifChangedNewVersion(t *testing.T, s blob.Store) {
	old := mustPut(t, s, "k", "v1", "")
	next := mustPut(t, s, "k", "v2", old)
	got, err := s.GetIfChanged(context.Background(), "k", old)
	if err != nil {
		t.Fatalf("GetIfChanged with an old etag: %v", err)
	}
	if string(got.Data) != "v2" || got.ETag != next {
		t.Fatalf("GetIfChanged = %q etag %q, want v2 etag %q", got.Data, got.ETag, next)
	}
}

func ifChangedMissing(t *testing.T, s blob.Store) {
	if _, err := s.GetIfChanged(context.Background(), "k", `"0123"`); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("GetIfChanged on a missing key: err = %v, want ErrNotFound", err)
	}
}

func ifChangedEmptyETag(t *testing.T, s blob.Store) {
	etag := mustPut(t, s, "k", "v1", "")
	got, err := s.GetIfChanged(context.Background(), "k", "")
	if err != nil || string(got.Data) != "v1" || got.ETag != etag {
		t.Fatalf("GetIfChanged(\"\") = %q etag %q, %v; want v1 etag %q", got.Data, got.ETag, err, etag)
	}
}

func ifChangedInvalidKeys(t *testing.T, s blob.Store) {
	for _, key := range InvalidKeys {
		if _, err := s.GetIfChanged(context.Background(), key, ""); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("GetIfChanged(%q): err = %v, want ErrInvalidKey", key, err)
		}
	}
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

// DeleteStore is a store that also deletes.
type DeleteStore interface {
	blob.Store
	blob.Deleter
}

// RunDeleter checks the delete contract (#658): удаленного больше нет, его
// ключ можно создать заново, повтор и отсутствующий ключ — не ошибка, плохой
// ключ отказан до всякого ввода-вывода.
func RunDeleter(t *testing.T, newStore func(t *testing.T) DeleteStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("deleted key is gone and can be created again", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Put(ctx, "m/f1", []byte("x"), ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "m/f1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "m/f1"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
		}
		if _, err := s.Put(ctx, "m/f1", []byte("y"), ""); err != nil {
			t.Fatalf("create after Delete: %v", err)
		}
	})
	t.Run("delete of a missing key is not an error", func(t *testing.T) {
		if err := newStore(t).Delete(ctx, "m/none"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete with an invalid key is refused", func(t *testing.T) {
		s := newStore(t)
		for _, key := range InvalidKeys {
			if err := s.Delete(ctx, key); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("Delete(%q): err = %v, want ErrInvalidKey", key, err)
			}
		}
	})
}

// InvalidPurgePrefixes are prefixes Purge must refuse before any I/O: пустой,
// корень, одна ступень, без «/» или «.» на конце, пустой сегмент, выход вверх.
var InvalidPurgePrefixes = []string{
	"", "/", "users/", "users", "users/a", "a/", "users//", "users//a/", "../a/", "users/../x/",
	"users/./a/", "/users/a/", "users\\a/", "users/a/\x00",
}

// PurgeStore is a store that also purges.
type PurgeStore interface {
	blob.Store
	blob.Purger
}

// RunPurger checks the purge contract (#545): уходит дерево человека и только
// оно — `users/a/` не задевает `users/a-b/` и `users/ab/`; префикс с точкой
// берет один файл; повтор и пустой префикс — 0 без ошибки; плохой префикс
// отказан и ничего не удалено.
func RunPurger(t *testing.T, newStore func(t *testing.T) PurgeStore) {
	t.Helper()
	ctx := context.Background()
	seed := func(t *testing.T, s PurgeStore) {
		t.Helper()
		for _, key := range []string{
			"users/a/kb/_data/catalog.json", "users/a/mail/log.jsonl",
			"users/a-b/kb/_data/catalog.json", "users/ab/kb/_data/catalog.json",
			"mailfiles/users/a/f1", "auth/seen/a.json", "auth/seen/ab.json",
		} {
			if _, err := s.Put(ctx, key, []byte(key), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	gone := func(t *testing.T, s PurgeStore, key string) bool {
		t.Helper()
		_, err := s.Get(ctx, key)
		if err != nil && !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Get %s: %v", key, err)
		}
		return err != nil
	}
	t.Run("removes the tree and only the tree", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		n, err := s.Purge(ctx, "users/a/")
		if err != nil || n != 2 {
			t.Fatalf("Purge = %d, %v; want 2, nil", n, err)
		}
		for _, key := range []string{"users/a/kb/_data/catalog.json", "users/a/mail/log.jsonl"} {
			if !gone(t, s, key) {
				t.Errorf("%s is still there", key)
			}
		}
		for _, key := range []string{"users/a-b/kb/_data/catalog.json", "users/ab/kb/_data/catalog.json", "mailfiles/users/a/f1", "auth/seen/a.json", "auth/seen/ab.json"} {
			if gone(t, s, key) {
				t.Errorf("%s was removed with the neighbour", key)
			}
		}
	})
	t.Run("a dot prefix takes one file", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		n, err := s.Purge(ctx, "auth/seen/a.")
		if err != nil || n != 1 {
			t.Fatalf("Purge = %d, %v; want 1, nil", n, err)
		}
		if !gone(t, s, "auth/seen/a.json") || gone(t, s, "auth/seen/ab.json") {
			t.Error("auth/seen/a. must take a.json and leave ab.json")
		}
	})
	t.Run("nothing under the prefix is zero, not an error", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		if n, err := s.Purge(ctx, "users/none/"); err != nil || n != 0 {
			t.Fatalf("Purge = %d, %v; want 0, nil", n, err)
		}
		if n, err := s.Purge(ctx, "users/a/"); err != nil || n != 2 {
			t.Fatalf("first Purge = %d, %v", n, err)
		}
		if n, err := s.Purge(ctx, "users/a/"); err != nil || n != 0 {
			t.Fatalf("second Purge = %d, %v; want 0, nil", n, err)
		}
	})
	t.Run("a bad prefix is refused and nothing is removed", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		for _, prefix := range InvalidPurgePrefixes {
			if _, err := s.Purge(ctx, prefix); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("Purge(%q): err = %v, want ErrInvalidKey", prefix, err)
			}
		}
		for _, key := range []string{"users/a/kb/_data/catalog.json", "users/ab/kb/_data/catalog.json", "auth/seen/a.json"} {
			if gone(t, s, key) {
				t.Errorf("%s was removed by a refused prefix", key)
			}
		}
	})
	t.Run("a purged key can be created again", func(t *testing.T) {
		s := newStore(t)
		seed(t, s)
		if _, err := s.Purge(ctx, "users/a/"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, "users/a/kb/_data/catalog.json", []byte("new"), ""); err != nil {
			t.Fatalf("create after Purge: %v", err)
		}
	})
}
