package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func TestRun_audit(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"Статья удалена","url":"https://h/","category":"golang","status":"keep"},
		{"id":2,"habr_id":2,"title":"Fresh take","url":"https://h/","category":"golang","status":"keep"}
	]}`)

	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "id=1") {
		t.Errorf("expected candidate id=1 in output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "id=2") {
		t.Errorf("clean entry id=2 should not be reported:\n%s", out.String())
	}
}

func TestRun_missingCatalog(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", "/no/such/file.json"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for missing catalog")
	}
}

func TestRun_catalogFlagRequired(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"audit"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit when --catalog is missing")
	}
}

func TestRun_unknownCheck(t *testing.T) {
	path := writeCatalog(t, `{"entries":[]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "bogus"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for unknown --check")
	}
}

func TestRun_checkFilter(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"Статья удалена","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "canonical"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	// outdated finding (id=1) must NOT appear when only canonical is selected
	if strings.Contains(out.String(), "id=1") {
		t.Errorf("canonical-only run leaked outdated finding:\n%s", out.String())
	}
}

func TestRun_unknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}

func TestRun_noArgs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit with no args")
	}
}
