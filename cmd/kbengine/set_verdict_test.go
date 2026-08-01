package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verdictCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"Dead link","url":"https://h/a","category":"golang","status":"consider","lifecycle":"dead-end","drift_http_code":404},
{"id":2,"title":"A draft of mine","url":"","category":"creations","status":"draft","lifecycle":"active","file":"creations/habr/x.md"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func statusOf(t *testing.T, path string, id int) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(entryMembers(t, path, id)["status"], &s); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	return s
}

// Five entries came out of the drift scan marked lifecycle: dead-end while
// their verdict still said «consider» — the base contradicting itself about
// links that answer 404. The catalog's own precedent (id 58) pairs a dead-end
// with skip-unavailable.
//
// This does NOT reopen --status. A raw status flag would write back the single
// legacy field that conflates verdict, read-state and publish stage. A verdict
// is one of those three, and writing it is what the loader already reads: a
// verdict implies the entry was read.
func TestSet_verdictWritesTheVerdict(t *testing.T) {
	path := verdictCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "1", "--verdict", "skip-unavailable"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if got := statusOf(t, path, 1); got != "skip-unavailable" {
		t.Fatalf("status = %q, want skip-unavailable", got)
	}
}

func TestSet_verdictRejectsWhatIsNotAVerdict(t *testing.T) {
	for _, bad := range []string{"read", "unread", "draft", "published", "active", "napodumat", "nonsense"} {
		path := verdictCatalog(t)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		var out, errb bytes.Buffer
		if code := run([]string{"set", "--catalog", path, "--ids", "1", "--verdict", bad}, &out, &errb); code == 0 {
			t.Errorf("set accepted --verdict %q, which is not a verdict", bad)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("a refused --verdict %q still wrote to the catalog", bad)
		}
	}
}

// A creation carries a publish stage, not a verdict. Writing one over the other
// would erase the stage and leave an entry the loader classifies as a different
// kind entirely.
func TestSet_verdictRefusesToOverwriteAPublishStage(t *testing.T) {
	path := verdictCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "2", "--verdict", "keep"}, &out, &errb); code == 0 {
		t.Fatal("set overwrote a publish stage with a verdict")
	}
	if !strings.Contains(errb.String(), "2") {
		t.Errorf("stderr %q does not name the entry it refused", errb.String())
	}
	if got := statusOf(t, path, 2); got != "draft" {
		t.Errorf("status = %q, want the publish stage untouched", got)
	}
}
