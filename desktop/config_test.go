package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadConfig_CreatesDefaultOnFirstRun(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "cfg", "config.json")

	cfg, err := loadConfig(path, home)
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
	cfg, err := loadConfig(path, home)
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
	if _, err := loadConfig(path, home); err == nil {
		t.Fatal("ждали ошибку разбора")
	}
}
