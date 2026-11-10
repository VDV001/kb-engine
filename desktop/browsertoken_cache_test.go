package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Находка повторного ревью #526: токен читался с диска на каждый кадр и каждое
// движение мыши. Читаем заново, только когда файл изменился; права и тип
// проверяются каждый раз (Lstat дешев), чтобы подмена файла не пряталась за кэшем.
func TestBrowserTokenCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "browser")
	first := strings.Repeat("a", 43)
	if err := os.WriteFile(path, []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var c browserTokenCache
	for range 3 {
		if got, err := c.read(path); err != nil || got != first {
			t.Fatalf("read = %q, %v; want %q", got, err, first)
		}
	}
	if c.reads != 1 {
		t.Fatalf("файл прочитан %d раз за три запроса, want 1", c.reads)
	}

	second := strings.Repeat("b", 44)
	if err := os.WriteFile(path, []byte(second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := c.read(path); err != nil || got != second {
		t.Fatalf("после смены файла read = %q, %v; want %q", got, err, second)
	}

	// Права открыли после кэширования: отказ, а не токен из памяти.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := c.read(path); err == nil {
		t.Fatalf("файл с правами 0644 принят из кэша: %q", got)
	}
}
