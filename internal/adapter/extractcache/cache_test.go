package extractcache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/extractcache"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 123, time.UTC)

// Текст файла берется из кеша, пока файл тот же: путь, размер и время
// изменения. Любое из трех поменялось — запись не подходит, файл распознается
// заново.
func TestCache_KeyIsPathSizeAndModTime(t *testing.T) {
	c := extractcache.Cache{Dir: filepath.Join(t.TempDir(), "extract")}
	if err := c.Put("/w/scan.png", 100, t0, "единорог"); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Text("/w/scan.png", 100, t0); !ok || got != "единорог" {
		t.Fatalf("Text = %q, %v", got, ok)
	}
	tests := []struct {
		name string
		path string
		size int64
		mod  time.Time
	}{
		{"другой путь", "/w/other.png", 100, t0},
		{"другой размер", "/w/scan.png", 101, t0},
		{"другое время", "/w/scan.png", 100, t0.Add(time.Nanosecond)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := c.Text(tt.path, tt.size, tt.mod); ok {
				t.Fatal("запись подошла к другому файлу")
			}
		})
	}
}

// В кеше текст личных файлов: папка 0700, записи 0600, как у index.gob.
func TestCache_IsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extract")
	c := extractcache.Cache{Dir: dir}
	if err := c.Put("/w/a.pdf", 1, t0, "текст"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("папка: %v, %v", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("записей %d, %v", len(entries), err)
	}
	if info, _ := entries[0].Info(); info.Mode().Perm() != 0o600 {
		t.Fatalf("запись: %v", info.Mode().Perm())
	}
	if strings.Contains(entries[0].Name(), "a.pdf") {
		t.Fatalf("путь файла виден в имени записи: %s", entries[0].Name())
	}
}

// Пустой текст — тоже ответ: картинка без надписей распознана, и распознавать
// ее снова на каждом проходе незачем.
func TestCache_EmptyTextIsAHit(t *testing.T) {
	c := extractcache.Cache{Dir: t.TempDir()}
	if err := c.Put("/w/photo.jpg", 5, t0, ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Text("/w/photo.jpg", 5, t0); !ok || got != "" {
		t.Fatalf("Text = %q, %v", got, ok)
	}
}

// Пустой Dir — кеша нет: ни одна запись не находится, Put ничего не пишет.
func TestCache_ZeroValueIsOff(t *testing.T) {
	var c extractcache.Cache
	if err := c.Put("/w/a.pdf", 1, t0, "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Text("/w/a.pdf", 1, t0); ok {
		t.Fatal("выключенный кеш ответил")
	}
}
