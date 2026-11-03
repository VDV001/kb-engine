package extractcache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/adapter/extractcache"
)

// fakeHelper кладет sh-скрипт на место kb-extract: настоящий помощник на Swift
// собирается только на macOS, а договор с ним — содержимое в stdin, вид файла
// вторым аргументом, текст в stdout и код выхода.
func fakeHelper(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kb-extract")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelper_Extract(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    string
		wantErr error // ждать эту ошибку
		anyErr  bool  // ждать любую ошибку
	}{
		// Файл приходит в stdin, а не путем: помощник не открывает ничего сам,
		// и подмена папки симлинком его не уводит (ревью #379).
		{"содержимое из stdin и вид файла", `[ "$1" = "-" ] || exit 9; printf '%s:' "$2"; cat`, "png:СЧЕТ", nil, false},
		// Хвост после кириллицы не срезается: «Итого: 5» терял «5» (ревью #379).
		{"хвост после кириллицы", `printf 'Итого: 5'`, "Итого: 5", nil, false},
		{"три байта на руну", `printf '———'`, "———", nil, false},
		{"пустая картинка", `exit 0`, "", nil, false},
		{"файл не открылся", `exit 2`, "", extractcache.ErrUnreadable, false},
		{"сбой помощника", `echo boom >&2; exit 1`, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := extractcache.Helper{Bin: fakeHelper(t, tt.script)}
			got, err := h.Extract(context.Background(), strings.NewReader("СЧЕТ"), "png")
			switch {
			case tt.anyErr:
				if err == nil {
					t.Fatal("сбой не назван")
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Fatalf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

// Зависший помощник не держит проход окна: после Timeout процесс убит, а
// ошибка называется таймаутом — это не «файл битый», и его стоит повторить.
func TestHelper_Timeout(t *testing.T) {
	h := extractcache.Helper{Bin: fakeHelper(t, `sleep 5; :`), Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := h.Extract(context.Background(), strings.NewReader(""), "pdf")
	if !errors.Is(err, extractcache.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("ждали %s", d)
	}
}

// Вывод помощника режется по MaxText на границе руны: текст скана в сотни
// страниц не раздувает ни память, ни кеш.
func TestHelper_CapsOutput(t *testing.T) {
	h := extractcache.Helper{Bin: fakeHelper(t, `yes ёёёёёёёё | head -c 4000000`)}
	got, err := h.Extract(context.Background(), strings.NewReader(""), "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > extractcache.MaxText || len(got) < extractcache.MaxText-utf8.UTFMax || !utf8.ValidString(got) {
		t.Fatalf("%d байт, valid=%v", len(got), utf8.ValidString(got))
	}
}

// Помощник ищется рядом с исполняемым файлом окна: kb.app/Contents/MacOS.
func TestHelperNextTo(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "kb")
	if _, ok := extractcache.HelperNextTo(exe); ok {
		t.Fatal("помощника нет, а найден")
	}
	if err := os.WriteFile(filepath.Join(dir, "kb-extract"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h, ok := extractcache.HelperNextTo(exe)
	if !ok || h.Bin != filepath.Join(dir, "kb-extract") {
		t.Fatalf("HelperNextTo = %+v, %v", h, ok)
	}
}
