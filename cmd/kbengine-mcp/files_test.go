package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Папки берутся из конфига окна: выбранное в kb-desktop и есть то, по чему
// ищет агент. Исключенная папка (finances) в выдачу не попадает, а снимок
// индекса ложится рядом с конфигом — тот же файл, что у окна.
func TestFileSearch_usesTheWindowConfig(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "notes")
	if err := os.MkdirAll(filepath.Join(root, "finances"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(root, "a.md"), "единорог в заметке"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(root, "finances", "b.md"), "единорог в деньгах"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "kb-desktop", "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{root}, Exclude: []string{"finances"}}); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	svc, err := newFileSearch(cfgPath, dir, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.Search("единорог", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Path() != filepath.Join(root, "a.md") {
		t.Fatalf("hits = %v, ждали только a.md", hits)
	}
	if _, err := os.Stat(desktopconfig.IndexPath(cfgPath)); err != nil {
		t.Fatalf("снимок индекса не рядом с конфигом: %v", err)
	}
}

func TestFileSearch_refusals(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	cfgGone := filepath.Join(dir, "gone.json")
	if err := desktopconfig.Write(cfgGone, desktopconfig.Config{Roots: []string{gone}}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		// Конфига нет — окно ни разу не запускалось. MCP умолчаний не
		// выдумывает и сам файл не создает: папки выбирает человек в окне.
		{"конфига нет", filepath.Join(dir, "none.json"), "kb-desktop"},
		// Ни одной доступной папки — отказ, а не пустой индекс: иначе агент
		// получал бы «ничего не найдено» по любому запросу.
		{"все папки пропали", cfgGone, "нет ни одной"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := newFileSearch(tt.path, dir, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, ждали со словом %q", err, tt.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "none.json")); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatal("конфиг создан читателем")
			}
		})
	}
}

// Пропавшая папка рядом с живой не роняет сервер, но называется вслух в
// stderr — тем же текстом, что у окна.
func TestFileSearch_namesSkippedRoots(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{dir, filepath.Join(dir, "gone")}}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if _, err := newFileSearch(cfgPath, dir, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "gone") {
		t.Fatalf("stderr молчит о пропавшей папке: %q", stderr.String())
	}
}
