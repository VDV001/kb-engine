package blobfs_test

import (
	"context"
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
