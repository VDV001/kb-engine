package blobfs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobtest"
)

func TestContract(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		s, err := blobfs.New(t.TempDir())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	})
}

// Отказанный ключ не должен оставить на диске ничего — ни файла рядом с
// корнем, ни каталога: проверка ключа стоит раньше любого mkdir.
func TestInvalidKeyTouchesNothingOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	s, err := blobfs.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, key := range blobtest.InvalidKeys {
		_, _ = s.Put(context.Background(), key, []byte("x"), "")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "root" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("parent of root holds %v, want only [root]", names)
	}
	inside, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 0 {
		t.Fatalf("refused keys left %d entries in root", len(inside))
	}
}

// Финансы лежат в том же дереве: файл и каталоги — только владельцу.
func TestWrittenFilesAreOwnerOnly(t *testing.T) {
	root := t.TempDir()
	s, err := blobfs.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Put(context.Background(), "users/u1/fin/journal.jsonl", []byte("{}"), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(root, "users"):                               0o700,
		filepath.Join(root, "users", "u1", "fin"):                  0o700,
		filepath.Join(root, "users", "u1", "fin", "journal.jsonl"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}

func TestNewRefusesEmptyRoot(t *testing.T) {
	if _, err := blobfs.New(""); err == nil {
		t.Fatal("New(\"\") succeeded: a store rooted at the working directory is a mistake, not a default")
	}
}

// Хранилищу нужен только свой корень: смонтированный том в контейнере или
// каталог под родителем только для чтения.
func TestPutNeedsOnlyTheRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	s, err := blobfs.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if _, err := s.Put(context.Background(), "k", []byte("v"), ""); err != nil {
		t.Fatalf("Put with a read-only parent: %v", err)
	}
}

// Имя замка внутри корня — не ключ: запись по нему сломала бы замок.
func TestLockNameIsNotAKey(t *testing.T) {
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), blobfs.LockName, []byte("x"), ""); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Put(%q): err = %v, want ErrInvalidKey", blobfs.LockName, err)
	}
}

// В бакете «a» и «a/b» — два независимых ключа, на диске один из них был бы
// каталогом. Вместо ошибок ОС — ошибки контракта.
func TestKeyCollidingWithADirectory(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/u1/kb/catalog.json", []byte("{}"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "users/u1/kb"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("Get of a directory: err = %v, want ErrNotFound", err)
	}
	if _, err := s.Put(ctx, "users/u1/kb", []byte("x"), ""); !errors.Is(err, blob.ErrInvalidKey) {
		t.Errorf("Put over a directory: err = %v, want ErrInvalidKey", err)
	}
	if _, err := s.Put(ctx, "users/u1/kb/catalog.json/inner", []byte("x"), ""); !errors.Is(err, blob.ErrInvalidKey) {
		t.Errorf("Put under a file: err = %v, want ErrInvalidKey", err)
	}
	if _, err := s.Get(ctx, "users/u1/kb/catalog.json/inner"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("Get under a file: err = %v, want ErrNotFound", err)
	}
}
