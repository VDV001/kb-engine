package localfiles_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
)

// Почему файл не прочитан — словами человека (окно DVK, «подробнее»):
// причина подсказывает, что с файлом сделать.
func TestExplain(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cp1251 := write("заметка.txt", []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}) // «Привет» в Windows-1251
	broken := write("отчет.docx", []byte("это не zip"))
	src, err := localfiles.New([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) error {
		_, err := src.ReadText(p)
		if err == nil {
			t.Fatalf("%s прочитан, а должен был отказать", p)
		}
		return err
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"кодировка", read(cp1251), "кодировка"},
		{"битый docx", read(broken), "Word"},
		{"нет прав", fmt.Errorf("open: %w", fs.ErrPermission), "нет прав"},
		{"прочее — с текстом ошибки", errors.New("diskio: boom"), "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localfiles.Explain(tc.err); !strings.Contains(got, tc.want) {
				t.Errorf("Explain(%v) = %q, want it to mention %q", tc.err, got, tc.want)
			}
		})
	}
}
