package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mixedVersionCatalog is the catalog as the legacy tools left it: someone
// else's material carrying version:1 (182 such entries live), a two-component
// default, and owner artefacts whose semver is already correct.
func mixedVersionCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"Someone else's article","url":"","category":"ai-agents-tools","status":"keep","lifecycle":"active","version":1},
{"id":2,"title":"Another one","url":"","category":"ai-agents-tools","status":"keep","lifecycle":"active","version":"1.0"},
{"id":3,"title":"A standard","url":"","category":"standards","status":"keep","lifecycle":"active","file":"standards/x/v1.md","version":"1.5.1"},
{"id":4,"title":"No version at all","url":"","category":"ai-agents-tools","status":"keep","lifecycle":"active"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func entryMembers(t *testing.T, path string, id int) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	var doc struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	for _, e := range doc.Entries {
		var got int
		if err := json.Unmarshal(e["id"], &got); err != nil {
			t.Fatalf("parse id: %v", err)
		}
		if got == id {
			return e
		}
	}
	t.Fatalf("no entry with id %d", id)
	return nil
}

// Without --apply the command reports the plan and leaves the file alone. 207
// entries change at once here; the owner gets to read what will happen first.
func TestMigrateVersions_dryRunWritesNothing(t *testing.T) {
	path := mixedVersionCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "versions", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry run rewrote the catalog")
	}
	if !strings.Contains(out.String(), "2") {
		t.Errorf("stdout %q does not report how many entries would change", out.String())
	}
}

func TestMigrateVersions_applyMovesForeignVersionsToRevision(t *testing.T) {
	path := mixedVersionCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "versions", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	for _, id := range []int{1, 2} {
		m := entryMembers(t, path, id)
		if _, still := m["version"]; still {
			t.Errorf("entry %d kept its version member", id)
		}
		var rev int
		if err := json.Unmarshal(m["revision"], &rev); err != nil {
			t.Fatalf("entry %d revision: %v", id, err)
		}
		if rev != 1 {
			t.Errorf("entry %d revision = %d, want 1", id, rev)
		}
	}

	// An owner artefact keeps its semver untouched, and an entry that never had
	// a version does not gain one: a migration that invents data is worse than
	// one that skips a case.
	if got := string(entryMembers(t, path, 3)["version"]); got != `"1.5.1"` {
		t.Errorf("entry 3 version = %s, want \"1.5.1\"", got)
	}
	if _, has := entryMembers(t, path, 3)["revision"]; has {
		t.Error("entry 3 gained a revision next to its version")
	}
	if _, has := entryMembers(t, path, 4)["revision"]; has {
		t.Error("entry 4 gained a revision out of nothing")
	}
}

// Running it twice must be safe: the second run finds nothing to do and says
// so, rather than failing or rewriting the file again.
func TestMigrateVersions_isIdempotent(t *testing.T) {
	path := mixedVersionCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "versions", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("first run exit = %d, stderr = %s", code, errb.String())
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"migrate", "versions", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("second run exit = %d, stderr = %s", code, errb.String())
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Error("second run changed the catalog again")
	}
	if !strings.Contains(out.String(), "нечего") {
		t.Errorf("stdout %q does not report that there was nothing to do", out.String())
	}
}

// An owner artefact whose version is a bare number cannot be widened by
// guesswork: 1 might mean 1.0.0 or the 1.2 written in its own title. The
// migration must refuse it by name instead of inventing a semver.
func TestMigrateVersions_refusesToGuessSemverForOwnArtefact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	doc := `{"entries":[
{"id":579,"title":"Deepread v1.2","url":"","category":"knowledge-management","status":"keep","lifecycle":"active","file":"docs/x_v1.md","version":1}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"migrate", "versions", "--catalog", path, "--apply"}, &out, &errb)
	if code == 0 {
		t.Fatal("migration accepted an own artefact with a bare number, want refusal")
	}
	if !strings.Contains(errb.String(), "579") {
		t.Errorf("stderr %q does not name the entry it cannot decide", errb.String())
	}
}
