package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Каталог с полем, которого домен не знает («exotic»), и с записью, у которой
// нет tags вовсе: обе ситуации живые, и обе должны пережить правку.
const setFixture = `{
  "meta": {"note": "top-level key the domain knows nothing about"},
  "entries": [
    {"id": 1, "title": "A", "url": "https://h/1/", "category": "golang", "status": "keep", "lifecycle": "canonical", "tags": ["go", "old"], "exotic": {"kept": true}},
    {"id": 2, "title": "B", "url": "https://h/2/", "category": "golang", "status": "keep", "lifecycle": "active"},
    {"id": 3, "title": "C", "url": "https://h/3/", "category": "golang", "status": "consider", "lifecycle": "active"}
  ],
  "last_updated": "2026-08-01"
}
`

func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(setFixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func load(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func entryByID(t *testing.T, doc map[string]any, id float64) map[string]any {
	t.Helper()
	for _, e := range doc["entries"].([]any) {
		m := e.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("entry %v not found", id)
	return nil
}

func TestSetFields_changesOnlyWhatWasAsked(t *testing.T) {
	path := writeFixture(t)

	n, err := catalogjson.SetFields(path, []int{2}, catalogjson.Changes{Lifecycle: "outdated"})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if n != 1 {
		t.Errorf("updated = %d, want 1", n)
	}

	doc := load(t, path)
	if got := entryByID(t, doc, 2)["lifecycle"]; got != "outdated" {
		t.Errorf("lifecycle = %v, want outdated", got)
	}
	if got := entryByID(t, doc, 1)["lifecycle"]; got != "canonical" {
		t.Errorf("neighbour changed: lifecycle = %v", got)
	}
	if got := entryByID(t, doc, 2)["title"]; got != "B" {
		t.Errorf("title lost: %v", got)
	}
}

// Главное требование. Существующие записи хранятся как raw JSON именно потому,
// что доменная проекция знает не все поля файла; правка не должна открывать
// дверь, которую append держит закрытой.
func TestSetFields_keepsUnknownFields(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{Lifecycle: "active"}); err != nil {
		t.Fatalf("SetFields: %v", err)
	}

	e := entryByID(t, load(t, path), 1)
	exotic, ok := e["exotic"].(map[string]any)
	if !ok || exotic["kept"] != true {
		t.Errorf("unknown field dropped: %v", e["exotic"])
	}
	if _, ok := e["tags"]; !ok {
		t.Error("tags dropped")
	}
}

// Ключи верхнего уровня, которые пишет старый python-дашборд, тоже не наши,
// и порядок их следования — то, что держит правку одной строки одной строкой
// в диффе.
func TestSetFields_keepsTopLevelKeysAndOrder(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{3}, catalogjson.Changes{Lifecycle: "dead-end"}); err != nil {
		t.Fatalf("SetFields: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"last_updated"`) || !strings.Contains(body, `"note"`) {
		t.Errorf("top-level keys lost:\n%s", body)
	}
	if strings.Index(body, `"meta"`) > strings.Index(body, `"entries"`) {
		t.Error("top-level order changed")
	}
}

func TestSetFields_tagsAddedAndRemovedWithoutDuplicates(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{
		AddTags:    []string{"new", "go"}, // «go» уже есть — дубля быть не должно
		RemoveTags: []string{"old"},
	}); err != nil {
		t.Fatalf("SetFields: %v", err)
	}

	tags := entryByID(t, load(t, path), 1)["tags"].([]any)
	got := make([]string, 0, len(tags))
	for _, t := range tags {
		got = append(got, t.(string))
	}
	want := []string{"go", "new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tags = %v, want %v", got, want)
	}
}

func TestSetFields_relatedIDsReplaceTheWholeList(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{2}, catalogjson.Changes{Related: []int{1, 3}}); err != nil {
		t.Fatalf("SetFields: %v", err)
	}

	rel := entryByID(t, load(t, path), 2)["related_ids"].([]any)
	if len(rel) != 2 || rel[0] != float64(1) || rel[1] != float64(3) {
		t.Errorf("related_ids = %v", rel)
	}
}

// Молчаливое «ничего не нашлось» — это тот же класс, что чинили в флагах:
// команда обязана сказать, что id не существует, а не отчитаться об успехе.
func TestSetFields_unknownIDIsAnErrorAndChangesNothing(t *testing.T) {
	path := writeFixture(t)
	before, _ := os.ReadFile(path)

	_, err := catalogjson.SetFields(path, []int{2, 999}, catalogjson.Changes{Lifecycle: "outdated"})
	if err == nil {
		t.Fatal("expected an error for a missing id")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("error does not name the missing id: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("file changed despite the error — the write must be all or nothing")
	}
}

func TestSetFields_rejectsAnUnknownLifecycle(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{Lifecycle: "не-такой"}); err == nil {
		t.Fatal("expected an error for an unknown lifecycle value")
	}
}

func TestSetFields_emptyChangeIsAnError(t *testing.T) {
	path := writeFixture(t)

	if _, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{}); err == nil {
		t.Fatal("expected an error when nothing was asked to change")
	}
}
