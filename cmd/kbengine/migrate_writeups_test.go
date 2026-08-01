package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeupBase lays out a base where `file` carries two different meanings at
// once — the defect this migration exists to remove:
//
//	id 1, 2 — someone else's articles (they have a url) whose `file` points at
//	          a shared write-up: that is "where my notes are", not identity
//	id 3    — the same, with a write-up of its own
//	id 4    — an own standard: here the file IS the entry's identity
//	id 5    — a deep-read of someone else's article, already modelled as its own
//	          entry (no url), which is exactly where the migration is heading
func writeupBase(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		"notes/batch9.md":         "# Batch 9 — разбор двадцати статей\n\nтекст\n",
		"notes/solo.md":           "# Solo — разбор одной статьи\n",
		"standards/harness/v1.md": "# Harness\nversion: 1.0.0\n",
		"docs/deepread.md":        "# Deepread\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "_data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := `{"entries":[
{"id":1,"title":"Чужая статья A","url":"https://h/a","category":"golang","status":"keep","lifecycle":"active","file":"notes/batch9.md"},
{"id":2,"title":"Чужая статья B","url":"https://h/b","category":"golang","status":"keep","lifecycle":"active","file":"notes/batch9.md","related_ids":[4]},
{"id":3,"title":"Чужая статья C","url":"https://h/c","category":"security","status":"consider","lifecycle":"active","file":"notes/solo.md"},
{"id":4,"title":"Мой стандарт","url":"","category":"standards","status":"read","lifecycle":"canonical","file":"standards/harness/v1.md","version":"1.0.0"},
{"id":5,"title":"Мой глубокий разбор","url":"","category":"claude-ecosystem","status":"read","lifecycle":"active","file":"docs/deepread.md"}
]}`
	path := filepath.Join(root, "_data", "catalog.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func entriesByID(t *testing.T, path string) map[int]map[string]any {
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

// Without --apply the plan is printed and the file is not touched — the same
// contract migrate versions gives.
func TestMigrateWriteups_planWritesNothing(t *testing.T) {
	path := writeupBase(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "writeups", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the plan wrote to the catalog")
	}
	if !strings.Contains(out.String(), "3") || !strings.Contains(out.String(), "2") {
		t.Errorf("the plan does not say 3 entries move onto 2 write-ups:\n%s", out.String())
	}
}

func TestMigrateWriteups_applySplitsTheTwoMeanings(t *testing.T) {
	path := writeupBase(t)

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "writeups", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	got := entriesByID(t, path)
	if len(got) != 7 {
		t.Fatalf("entries = %d, want 7 (5 + one per write-up)", len(got))
	}

	// the write-up entries carry the file and take their title from it
	var batch9, solo int
	for id, e := range got {
		if e["file"] == "notes/batch9.md" && id > 5 {
			batch9 = id
		}
		if e["file"] == "notes/solo.md" && id > 5 {
			solo = id
		}
	}
	if batch9 == 0 || solo == 0 {
		t.Fatalf("no entry was created for one of the write-ups: %v", got)
	}
	if title, _ := got[batch9]["title"].(string); !strings.Contains(title, "Batch 9") {
		t.Errorf("write-up title = %q, want the heading from the file", title)
	}
	if cat, _ := got[batch9]["category"].(string); cat != "writeups" {
		t.Errorf("write-up category = %q, want writeups", cat)
	}

	// the articles no longer carry a file, and point at their write-up instead
	for _, id := range []int{1, 2, 3} {
		if f, ok := got[id]["file"]; ok && f != "" {
			t.Errorf("entry %d still carries file %v", id, f)
		}
	}
	if !hasRelated(got[1], batch9) || !hasRelated(got[2], batch9) || !hasRelated(got[3], solo) {
		t.Errorf("an article does not point at its write-up: %v %v %v",
			got[1]["related_ids"], got[2]["related_ids"], got[3]["related_ids"])
	}
	// an existing related link survives
	if !hasRelated(got[2], 4) {
		t.Errorf("entry 2 lost its existing related link: %v", got[2]["related_ids"])
	}

	// own artefacts are left exactly as they were
	if got[4]["file"] != "standards/harness/v1.md" || got[5]["file"] != "docs/deepread.md" {
		t.Errorf("an own artefact lost its file: %v %v", got[4]["file"], got[5]["file"])
	}
}

// Running it twice must not create a second copy of every write-up.
func TestMigrateWriteups_isIdempotent(t *testing.T) {
	path := writeupBase(t)
	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "writeups", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("first run: exit = %d, %s", code, errb.String())
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	out.Reset()
	if code := run([]string{"migrate", "writeups", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("second run: exit = %d, %s", code, errb.String())
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the second run changed the catalog again")
	}
	if !strings.Contains(out.String(), "нечего") {
		t.Errorf("the second run does not say there is nothing to move:\n%s", out.String())
	}
}

func hasRelated(entry map[string]any, want int) bool {
	raw, ok := entry["related_ids"].([]any)
	if !ok {
		return false
	}
	for _, v := range raw {
		if n, ok := v.(float64); ok && int(n) == want {
			return true
		}
	}
	return false
}
