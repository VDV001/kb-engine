package runlogjsonl_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
)

// Отсутствие файла и пустой файл — разные ответы. Load отдаёт ноль записей в
// обоих случаях, поэтому различить их может только отдельный вопрос.
func TestExists(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "нет.jsonl")

	got, err := runlogjsonl.Exists(missing)
	if err != nil {
		t.Fatalf("Exists(отсутствует): %v", err)
	}
	if got {
		t.Error("файла нет, а Exists говорит, что есть")
	}

	empty := filepath.Join(dir, "пустой.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = runlogjsonl.Exists(empty)
	if err != nil {
		t.Fatalf("Exists(пустой): %v", err)
	}
	if !got {
		t.Error("файл есть и пуст, а Exists говорит, что его нет")
	}
}
