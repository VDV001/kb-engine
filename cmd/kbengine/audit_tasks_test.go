package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	body := `{"entries":[
		{"id":1,"title":"Present","url":"https://habr.com/ru/articles/1024770/","category":"golang","status":"keep"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func TestRunAuditTasks_orphan(t *testing.T) {
	path := writeTaskCatalog(t)
	// Task 99 is completed but references habr 555, which is NOT in the catalog.
	stdin := strings.NewReader("#99 [completed] habr 555 missing entry\n")

	var out, errb bytes.Buffer
	code := runAuditTasks([]string{"--catalog", path}, stdin, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for orphan; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "555") {
		t.Errorf("expected orphan habr 555 in output:\n%s", out.String())
	}
}

func TestRunAuditTasks_consistent(t *testing.T) {
	path := writeTaskCatalog(t)
	stdin := strings.NewReader("#10 [completed] habr 1024770 present entry\n")

	var out, errb bytes.Buffer
	code := runAuditTasks([]string{"--catalog", path}, stdin, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for consistent; stderr=%s", code, errb.String())
	}
}

func TestRunAuditTasks_emptyStdin(t *testing.T) {
	path := writeTaskCatalog(t)
	var out, errb bytes.Buffer
	code := runAuditTasks([]string{"--catalog", path}, strings.NewReader("   "), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for empty stdin", code)
	}
}

func TestRunAuditTasks_catalogRequired(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runAuditTasks(nil, strings.NewReader("x"), &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit when --catalog missing")
	}
}

func TestRun_routesAuditTasks(t *testing.T) {
	// The dispatcher must know the command (it reads os.Stdin, here empty → 2).
	var out, errb bytes.Buffer
	path := writeTaskCatalog(t)
	if code := run([]string{"audit-tasks", "--catalog", path}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit (empty os.Stdin) but command must be recognized")
	}
	if strings.Contains(errb.String(), "unknown command") {
		t.Errorf("audit-tasks not routed: %s", errb.String())
	}
}
