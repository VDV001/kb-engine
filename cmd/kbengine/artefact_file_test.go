package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func memberString(t *testing.T, path string, id int, member string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(entryMembers(t, path, id)[member], &s); err != nil {
		t.Fatalf("parse %s: %v", member, err)
	}
	return s
}

// baseWithCatalog lays out a knowledge base the way the real one is laid out:
// the catalog under _data/, the write-ups beside it. The artefact root is
// derived from the catalog path, which is how the audit already resolves them.
func baseWithCatalog(t *testing.T, existing ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "_data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, rel := range existing {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("# write-up\n"), 0o600); err != nil {
			t.Fatalf("write artefact: %v", err)
		}
	}
	path := filepath.Join(root, "_data", "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"Entry one","url":"https://h/a","category":"golang","status":"consider","lifecycle":"active"},
{"id":2,"title":"Entry two","url":"https://h/b","category":"golang","status":"consider","lifecycle":"active","file":"notes/gone.md"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

// A path that points at nothing is the defect twelve entries already carried:
// the write-up was renamed or never existed, and nothing said so. Refusing on
// the way in is what keeps them from coming back.
func TestSet_fileRejectsAPathThatPointsAtNothing(t *testing.T) {
	path := baseWithCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"set", "--catalog", path, "--ids", "1", "--file", "notes/absent.md"}, &out, &errb)

	if code == 0 {
		t.Errorf("set accepted a file that does not exist")
	}
	if !strings.Contains(errb.String(), "notes/absent.md") {
		t.Errorf("the message does not name the path it looked for: %q", errb.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused --file still wrote to the catalog")
	}
}

func TestSet_fileAcceptsAPathThatExists(t *testing.T) {
	path := baseWithCatalog(t, "notes/present.md")

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "1", "--file", "notes/present.md"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if got := memberString(t, path, 1, "file"); got != "notes/present.md" {
		t.Errorf("file = %q, want notes/present.md", got)
	}
}

// add is the same door: there the file is the entry's identity, so accepting
// one that does not exist is worse than on set.
func TestAdd_rejectsAPathThatPointsAtNothing(t *testing.T) {
	path := baseWithCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"add", "--catalog", path, "--title", "Mine", "--category", "creations", "--file", "notes/absent.md"}, &out, &errb)

	if code == 0 {
		t.Errorf("add accepted a file that does not exist")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused add still wrote to the catalog")
	}
}

func TestAdd_acceptsAPathThatExists(t *testing.T) {
	path := baseWithCatalog(t, "creations/mine.md")

	var out, errb bytes.Buffer
	if code := run([]string{"add", "--catalog", path, "--title", "Mine", "--category", "creations", "--file", "creations/mine.md"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
}

// Closing the door does nothing about what is already inside: entry 2 points at
// a write-up that is gone, and until now only a hand-written script could say
// so. The audit has to be able to name it.
func TestAudit_filesNamesEntriesWhoseWriteUpIsGone(t *testing.T) {
	path := baseWithCatalog(t, "notes/present.md")

	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "files"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "id=2") || !strings.Contains(got, "notes/gone.md") {
		t.Errorf("audit did not name the entry with the missing write-up:\n%s", got)
	}
	if strings.Contains(got, "id=1") {
		t.Errorf("audit reported an entry that carries no file at all:\n%s", got)
	}
}
