package desktopconfig_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func TestLoadConfig_CreatesDefaultOnFirstRun(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "cfg", "config.json")

	cfg, err := desktopconfig.Load(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Roots, []string{filepath.Join(home, "claude-cowork")}) {
		t.Errorf("roots = %q", cfg.Roots)
	}
	// finances не индексируется по умолчанию: личные деньги не должны попасть
	// в индекс, который отдается агенту по MCP.
	if !slices.Contains(cfg.Exclude, "finances") {
		t.Errorf("exclude = %q, want finances", cfg.Exclude)
	}
	if cfg.Catalog != filepath.Join(home, "claude-cowork", "knowledge-base", "_data", "catalog.json") {
		t.Errorf("catalog = %q", cfg.Catalog)
	}
	// Первый запуск записывает файл, чтобы человеку было что править.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("конфиг не записан: %v", err)
	}
}

func TestLoadConfig_ReadsExisting(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(`{"roots":["/data"],"exclude":[],"catalog":"","server":"https://kb.example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Load(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Roots, []string{"/data"}) || cfg.Server != "https://kb.example" || cfg.Catalog != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadConfig_BrokenFileIsAnError(t *testing.T) {
	// Испорченный конфиг — отказ, а не тихий возврат к умолчаниям: иначе окно
	// молча проиндексирует не те папки, а человек будет думать, что свои.
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(`{"roots":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopconfig.Load(path, home); err == nil {
		t.Fatal("ждали ошибку разбора")
	}
}

func TestRead_MissingFileIsNotExist(t *testing.T) {
	// Читатель без права на умолчания (MCP-сервер) должен отличать «окно ни
	// разу не запускалось» от испорченного файла и не создавать конфиг сам.
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := desktopconfig.Read(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("Read создал конфиг")
	}
}

func TestNormalizeRoots(t *testing.T) {
	// «~» в конфиге и удаленная папка роняли все окно через log.Fatal (ревью
	// #371). Тильда раскрывается, несуществующее пропускается и называется.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots, skipped := desktopconfig.NormalizeRoots([]string{"~/notes", filepath.Join(home, "gone"), "relative"}, home)
	if len(roots) != 1 || roots[0] != filepath.Join(home, "notes") {
		t.Errorf("roots = %q", roots)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %q, want 2", skipped)
	}
}

// Распознанный текст лежит рядом с конфигом и снимком индекса: окно пишет,
// MCP читает тот же каталог.
func TestExtractDir_NextToConfig(t *testing.T) {
	if got, want := desktopconfig.ExtractDir("/a/kb-desktop/config.json"), "/a/kb-desktop/extract"; got != want {
		t.Fatalf("ExtractDir = %q, want %q", got, want)
	}
}

// Запись заменяет файл целиком: читатель в другом процессе (MCP) не видит
// конфиг, обрезанный на середине записи.
func TestWrite_ReaderNeverSeesHalfFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	cfg := desktopconfig.Default(home)
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		defer close(stop)
		for range 2000 {
			if err := desktopconfig.Write(path, cfg); err != nil {
				t.Error(err)
				return
			}
		}
	})
	bad := 0
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := desktopconfig.Read(path); err != nil {
				bad++
			}
		}
	})
	wg.Wait()
	if bad > 0 {
		t.Fatalf("читатель %d раз увидел недописанный конфиг", bad)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("права = %v, %v", fi.Mode().Perm(), err)
	}
	left, _ := filepath.Glob(filepath.Join(home, "*.tmp*"))
	if len(left) > 0 {
		t.Fatalf("остались временные файлы: %v", left)
	}
}
