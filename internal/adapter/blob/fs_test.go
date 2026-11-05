package blob_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

func newFS(t *testing.T) (blob.Store, fs.FS) {
	t.Helper()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := blob.FS(context.Background(), s, "users/u1/kb/")
	if err != nil {
		t.Fatal(err)
	}
	return s, fsys
}

func TestFSReadsUnderThePrefix(t *testing.T) {
	s, fsys := newFS(t)
	if _, err := s.Put(context.Background(), "users/u1/kb/notes/2026-11-05_тема.md", []byte("# Тема\n"), ""); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Open("notes/2026-11-05_тема.md")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if st.Name() != "2026-11-05_тема.md" || st.Size() != int64(len("# Тема\n")) || st.IsDir() {
		t.Fatalf("Stat = name %q size %d dir %v", st.Name(), st.Size(), st.IsDir())
	}
	// /kb/ отдает файл через http.ServeContent — ему нужен Seek.
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		t.Fatal("file is not an io.ReadSeeker")
	}
	if _, err := rs.Seek(2, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(rs)
	if err != nil || string(rest) != "Тема\n" {
		t.Fatalf("after Seek(2) read %q, %v", rest, err)
	}
}

// Ключ другого пользователя за пределами префикса недостижим даже по точному
// пути: fs.ValidPath отказывает «..», а префикс приклеивается всегда.
func TestFSStaysInsideThePrefix(t *testing.T) {
	s, fsys := newFS(t)
	if _, err := s.Put(context.Background(), "users/u2/fin/journal.jsonl", []byte("secret"), ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../../u2/fin/journal.jsonl", "/users/u2/fin/journal.jsonl", "users/u2/fin/journal.jsonl"} {
		f, err := fsys.Open(name)
		if err == nil {
			_ = f.Close()
			t.Fatalf("Open(%q) succeeded", name)
		}
	}
}

func TestFSMissingIsErrNotExist(t *testing.T) {
	_, fsys := newFS(t)
	if _, err := fsys.Open("notes/none.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open missing: err = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Open("../x"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Open(../x): err = %v, want fs.ErrInvalid", err)
	}
}

func TestFSRefusesABadPrefix(t *testing.T) {
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"users/u1", "../x/", "/abs/"} {
		if _, err := blob.FS(context.Background(), s, p); err == nil {
			t.Errorf("FS(prefix %q) accepted", p)
		}
	}
}
