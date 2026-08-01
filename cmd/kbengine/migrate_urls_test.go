package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trackedCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"From the digest","url":"https://habr.com/ru/articles/1/?utm_campaign=1&utm_source=habrahabr&utm_medium=rss","category":"golang","status":"keep","lifecycle":"active"},
{"id":2,"title":"Clean already","url":"https://habr.com/ru/articles/2/","category":"golang","status":"keep","lifecycle":"active"},
{"id":3,"title":"Unknown parameter","url":"https://stitch.withgoogle.com/p/7?pli=1","category":"golang","status":"keep","lifecycle":"active"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func urlOf(t *testing.T, path string, id int) string {
	t.Helper()
	m := entryMembers(t, path, id)
	var u string
	if err := json.Unmarshal(m["url"], &u); err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u
}

func TestMigrateURLs_dryRunWritesNothing(t *testing.T) {
	path := trackedCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "urls", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry run rewrote the catalog")
	}
	// The plan must show the address as it will become, not merely a count:
	// this rewrites what entries ARE, and it should be readable before it runs.
	if !strings.Contains(out.String(), "https://habr.com/ru/articles/1/") {
		t.Errorf("plan does not show the resulting address:\n%s", out.String())
	}
}

func TestMigrateURLs_applyStripsOnlyTracking(t *testing.T) {
	path := trackedCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "urls", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if got := urlOf(t, path, 1); got != "https://habr.com/ru/articles/1/" {
		t.Errorf("entry 1 url = %q, want the tail removed", got)
	}
	if got := urlOf(t, path, 2); got != "https://habr.com/ru/articles/2/" {
		t.Errorf("entry 2 url = %q, want it untouched", got)
	}
	if got := urlOf(t, path, 3); got != "https://stitch.withgoogle.com/p/7?pli=1" {
		t.Errorf("entry 3 url = %q, want the unknown parameter kept", got)
	}
}

func TestMigrateURLs_isIdempotent(t *testing.T) {
	path := trackedCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "urls", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("first run: %s", errb.String())
	}
	out.Reset()
	if code := run([]string{"migrate", "urls", "--catalog", path, "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("second run: %s", errb.String())
	}
	if !strings.Contains(out.String(), "нечего") {
		t.Errorf("second run did not report that there was nothing to do:\n%s", out.String())
	}
}
