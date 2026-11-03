package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestFileKindOf(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/a/notes.md", "doc"},
		{"/a/README.MD", "doc"},
		{"/a/plan.txt", "doc"},
		{"/a/page.html", "doc"},
		{"/a/main.go", "code"},
		{"/a/app.tsx", "code"},
		{"/a/run.sh", "code"},
		{"/a/data.json", "code"},
		{"/a/shot.png", "image"},
		{"/a/photo.JPG", "image"},
		{"/a/archive.zip", "other"},
		{"/a/Makefile", "other"},
	} {
		if got := domain.FileKindOf(tc.path).String(); got != tc.want {
			t.Errorf("FileKindOf(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestNewFileDoc(t *testing.T) {
	mod := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		path    string
		size    int64
		wantErr error
	}{
		{"абсолютный путь", "/home/u/notes.md", 10, nil},
		{"относительный путь", "notes.md", 10, domain.ErrInvalidFilePath},
		{"пустой путь", "", 10, domain.ErrInvalidFilePath},
		{"неочищенный путь", "/home/u/../u/notes.md", 10, domain.ErrInvalidFilePath},
		{"отрицательный размер", "/home/u/notes.md", -1, domain.ErrInvalidFileSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := domain.NewFileDoc(tc.path, tc.size, mod)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if d.Path() != tc.path || d.Size() != tc.size || !d.ModTime().Equal(mod) {
				t.Fatalf("поля не сохранились: %+v", d)
			}
			if d.Name() != "notes.md" || d.Kind().String() != "doc" {
				t.Fatalf("Name/Kind = %q/%q", d.Name(), d.Kind())
			}
		})
	}
}
