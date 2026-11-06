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
	page := write("tz.docx", []byte("\n  <!DOCTYPE html>\n<html lang=\"ru\"><head><title>Вход</title>"))
	oldDoc := write("старый.docx", append([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, make([]byte, 64)...))
	pdf := write("скан.docx", []byte("%PDF-1.7\n%âãÏÓ"))
	empty := write("пусто.docx", nil)
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
		// Чем файл оказался на самом деле — по содержимому, а не по расширению.
		{"веб-страница под .docx", read(page), "веб-страница"},
		{"старый .doc под .docx", read(oldDoc), "старый формат"},
		{"PDF под .docx", read(pdf), "PDF"},
		{"пустой файл", read(empty), "пустой"},
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

// У веб-страницы под видом .docx в подробностях — ее заголовок: видно, что
// скачалось (у владельца — страница входа площадки вместо ТЗ).
func TestExplain_webPageTitleInDetail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tz.docx")
	if err := os.WriteFile(p, []byte("<!DOCTYPE html><html><head><title>\n  Вход   на площадку </title></head>"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := localfiles.New([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.ReadText(p)
	if err == nil || !strings.Contains(err.Error(), "«Вход на площадку»") {
		t.Fatalf("ошибка %v: нет заголовка страницы", err)
	}
}
