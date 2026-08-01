package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

func writeupCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"Статья A","url":"https://h/a","category":"golang","status":"keep","lifecycle":"active","file":"notes/batch.md"},
{"id":2,"title":"Статья B","url":"https://h/b","category":"golang","status":"keep","lifecycle":"active","file":"notes/batch.md","related_ids":[3]},
{"id":3,"title":"Мой стандарт","url":"","category":"standards","status":"read","lifecycle":"canonical","file":"standards/x/v1.md"},
{"id":4,"title":"Спасённая","url":"https://h/d","category":"golang","status":"keep","lifecycle":"active","file":"notes/rescued/4_dead.md"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC) }
}

func titles(_ string) string { return "Разбор партии" }

func TestMigrateWriteups_planDoesNotWrite(t *testing.T) {
	path := writeupCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	plan, err := catalogjson.MigrateWriteups(path, titles, fixedClock(), false)
	if err != nil {
		t.Fatalf("MigrateWriteups: %v", err)
	}
	if plan.Moved != 2 || len(plan.Created) != 1 {
		t.Errorf("plan = %d moved / %d created, want 2 / 1", plan.Moved, len(plan.Created))
	}
	if plan.Created[0].Count != 2 {
		t.Errorf("write-up cited by %d, want 2", plan.Created[0].Count)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the plan wrote to the catalog")
	}
}

func TestMigrateWriteups_applyRewritesTheEntries(t *testing.T) {
	path := writeupCatalog(t)

	plan, err := catalogjson.MigrateWriteups(path, titles, fixedClock(), true)
	if err != nil {
		t.Fatalf("MigrateWriteups: %v", err)
	}
	newID := plan.Created[0].ID
	if newID != 5 {
		t.Errorf("new entry id = %d, want 5 (one past the highest)", newID)
	}

	entries := decodeEntries(t, path)
	if _, ok := entries[1]["file"]; ok {
		t.Error("entry 1 kept its file")
	}
	if !containsID(entries[2]["related_ids"], 3) || !containsID(entries[2]["related_ids"], newID) {
		t.Errorf("entry 2 links = %v, want both the old link and the write-up", entries[2]["related_ids"])
	}
	if entries[3]["file"] != "standards/x/v1.md" {
		t.Errorf("own artefact lost its file: %v", entries[3]["file"])
	}
	if entries[4]["file"] != "notes/rescued/4_dead.md" {
		t.Errorf("rescued copy was moved: %v", entries[4]["file"])
	}
	if entries[newID]["category"] != "writeups" || entries[newID]["date_added"] != "2026-08-02" {
		t.Errorf("write-up entry = %v", entries[newID])
	}
}

func TestMigrateWriteups_nothingToDoIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := catalogjson.MigrateWriteups(path, titles, fixedClock(), true)
	if err != nil {
		t.Fatalf("MigrateWriteups: %v", err)
	}
	if plan.Moved != 0 || len(plan.Created) != 0 {
		t.Errorf("plan = %v, want empty", plan)
	}
}

func decodeEntries(t *testing.T, path string) map[int]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := make(map[int]map[string]any, len(doc.Entries))
	for _, e := range doc.Entries {
		out[int(e["id"].(float64))] = e
	}
	return out
}

func containsID(raw any, want int) bool {
	list, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, v := range list {
		if n, ok := v.(float64); ok && int(n) == want {
			return true
		}
	}
	return false
}
