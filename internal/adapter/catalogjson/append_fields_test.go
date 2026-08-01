package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
)

// Добавленная запись обязана сохранить путь к файлу и версию. Пока они
// терялись, дедуп по файлу не находил ничего: в файле поля просто не было,
// и один и тот же стандарт добавлялся сколько угодно раз.
func TestAppendEntries_keepsFileAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries": [], "last_updated": "2026-08-01"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cat, _ := domain.NewCategory("standards")
	life, _ := domain.NewLifecycle("canonical")
	read, _ := domain.NewReadState("read")
	ver, err := domain.NewVersion("1.3.0")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: 1, Kind: domain.KindArticle, Title: "Стандарт", Category: cat,
		Lifecycle: life, ReadState: &read,
		NotesFile: "standards/harness-engineering-defaults/v1.md", Version: &ver,
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}

	if err := catalogjson.AppendEntries(path, []domain.Entry{e}); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("записей = %d, want 1", len(doc.Entries))
	}
	if got := doc.Entries[0]["file"]; got != "standards/harness-engineering-defaults/v1.md" {
		t.Errorf("file = %v — путь потерян при записи", got)
	}
	if got := doc.Entries[0]["version"]; got != "1.3.0" {
		t.Errorf("version = %v — версия потеряна при записи", got)
	}
}

// Обратная сторона: у записи без файла и версии эти ключи не должны появляться
// пустыми — иначе каталог обрастёт полями, которых у записи нет.
func TestAppendEntries_omitsEmptyFileAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries": [], "last_updated": "2026-08-01"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cat, _ := domain.NewCategory("golang")
	life, _ := domain.NewLifecycle("active")
	read, _ := domain.NewReadState("unread")
	e, err := domain.NewEntry(domain.EntryParams{
		ID: 2, Kind: domain.KindArticle, Title: "Ссылка", Category: cat,
		Lifecycle: life, ReadState: &read, URL: "https://h/1/",
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}

	if err := catalogjson.AppendEntries(path, []domain.Entry{e}); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	_ = json.Unmarshal(raw, &doc)
	if _, ok := doc.Entries[0]["file"]; ok {
		t.Error("пустой file попал в запись")
	}
	if _, ok := doc.Entries[0]["version"]; ok {
		t.Error("пустая version попала в запись")
	}
}
