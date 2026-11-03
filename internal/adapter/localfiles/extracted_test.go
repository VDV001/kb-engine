package localfiles_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/domain"
)

// fakeExtracted — кеш распознанного текста в памяти, тот же ключ, что у
// настоящего: путь, размер, время изменения.
type fakeExtracted map[string]string

func extKey(path string, size int64, mod time.Time) string {
	return fmt.Sprintf("%s|%d|%d", path, size, mod.UnixNano())
}

func (f fakeExtracted) Text(path string, size int64, mod time.Time) (string, bool) {
	t, ok := f[extKey(path, size, mod)]
	return t, ok
}

// cache кладет в f текст для файла на диске.
func (f fakeExtracted) cache(t *testing.T, path, text string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f[extKey(path, info.Size(), info.ModTime())] = text
}

func pending(t *testing.T, s *localfiles.Source) []string {
	t.Helper()
	var out []string
	err := s.Pending(context.Background(), func(d domain.FileDoc, open string) error {
		if _, err := os.Stat(open); err != nil {
			t.Fatalf("путь для помощника не открывается: %v", err)
		}
		out = append(out, d.Path())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// PDF и картинка попадают в индекс, когда их текст уже распознан окном; до
// этого их нет ни в обходе, ни в чтении, но они перечислены как ждущие.
func TestExtracted_OnlyWhenCached(t *testing.T) {
	root := t.TempDir()
	scan := write(t, root, "scans/scan.png", "png")
	photo := write(t, root, "scans/photo.jpg", "jpg")
	report := write(t, root, "docs/report.pdf", "%PDF")
	old := write(t, root, "docs/old.pdf", "%PDF")
	note := write(t, root, "docs/note.md", "заметка")
	ext := fakeExtracted{}
	ext.cache(t, scan, "СЧЕТ НА ОПЛАТУ")
	ext.cache(t, report, "техническое задание")
	s, err := localfiles.New([]string{root}, nil, localfiles.WithExtracted(ext))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{report, note, scan}
	slices.Sort(want)
	if got := walked(t, s); !slices.Equal(got, want) {
		t.Fatalf("walked = %q, want %q", got, want)
	}
	if got, err := s.ReadText(scan); err != nil || got != "СЧЕТ НА ОПЛАТУ" {
		t.Fatalf("ReadText(scan) = %q, %v", got, err)
	}
	if got, err := s.ReadText(report); err != nil || got != "техническое задание" {
		t.Fatalf("ReadText(report) = %q, %v", got, err)
	}
	if _, err := s.ReadText(photo); err == nil {
		t.Fatal("нераспознанная картинка прочиталась")
	}
	wantPending := []string{old, photo}
	slices.Sort(wantPending)
	if got := pending(t, s); !slices.Equal(got, wantPending) {
		t.Fatalf("pending = %q, want %q", got, wantPending)
	}
}

// Без кеша PDF и картинки не индексируются и не ждут: источник без окна
// (Linux, Docker, kbengine files) работает как прежде.
func TestExtracted_OffWithoutCache(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.png", "png")
	write(t, root, "b.pdf", "%PDF")
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); len(got) != 0 {
		t.Fatalf("walked = %q", got)
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("pending = %q", got)
	}
}

// Папка, где картинок больше MaxImagesPerDir, — кадры роликов или рендеры:
// ее картинки не распознаются и не индексируются. PDF в такой папке — да.
func TestExtracted_ImageFolderCap(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		images int
		want   int // картинок в обходе и в ожидании
	}{
		{"на пороге", localfiles.MaxImagesPerDir, localfiles.MaxImagesPerDir},
		{"сверх порога", localfiles.MaxImagesPerDir + 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(root, tt.name)
			ext := fakeExtracted{}
			for i := range tt.images {
				ext.cache(t, write(t, dir, fmt.Sprintf("f%04d.png", i), "png"), "кадр")
				write(t, dir, fmt.Sprintf("u%04d.txt", i), "текст") // не картинки не в счет
			}
			pdf := write(t, dir, "doc.pdf", "%PDF")
			s, err := localfiles.New([]string{dir}, nil, localfiles.WithExtracted(ext))
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, p := range walked(t, s) {
				if filepath.Ext(p) == ".png" {
					n++
				}
			}
			if n != tt.want {
				t.Fatalf("картинок в обходе %d, ждали %d", n, tt.want)
			}
			if got := pending(t, s); !slices.Equal(got, []string{pdf}) {
				t.Fatalf("pending = %d файлов, ждали только pdf", len(got))
			}
			if tt.want == 0 {
				if _, err := s.ReadText(filepath.Join(dir, "f0000.png")); err == nil {
					t.Fatal("картинка из папки кадров прочиталась")
				}
			}
		})
	}
}
