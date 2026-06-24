package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

func TestRun_inbox(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"meta":{},"entries":[
		{"id":1,"title":"Old","url":"https://habr.com/ru/articles/dup/","category":"golang","status":"keep"}
	]}`), 0o600); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	inboxDir := filepath.Join(dir, "inbox")
	processedDir := filepath.Join(dir, "processed")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	// One file: a duplicate (existing URL) and a fresh article.
	inboxFile := filepath.Join(inboxDir, "batch.json")
	if err := os.WriteFile(inboxFile, []byte(`[
		{"title":"Dup","url":"https://habr.com/ru/articles/dup/","hub":"go","tags":["x"],"createdAt":"2026-06-01T00:00:00.000Z"},
		{"title":"Fresh","url":"https://habr.com/ru/articles/new/","hub":"reactjs","tags":["react"],"createdAt":"2026-06-02T00:00:00.000Z"}
	]`), 0o600); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"inbox", "--catalog", catalogPath, "--inbox", inboxDir, "--processed", processedDir}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "added 1") {
		t.Errorf("expected 'added 1' in output:\n%s", out.String())
	}

	// Catalog grew by exactly the fresh article (duplicate skipped).
	c, err := catalogjson.Load(catalogPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("catalog Len = %d, want 2", c.Len())
	}
	got, ok := c.Find(2)
	if !ok || got.Title() != "Fresh" || got.Category().String() != "frontend" {
		t.Errorf("fresh entry not added correctly: ok=%v entry=%+v", ok, got)
	}

	// The processed file moved out of the inbox.
	leftover, _ := filepath.Glob(filepath.Join(inboxDir, "*.json"))
	if len(leftover) != 0 {
		t.Errorf("inbox still has files: %v", leftover)
	}
	moved, _ := filepath.Glob(filepath.Join(processedDir, "*.json"))
	if len(moved) != 1 {
		t.Errorf("processed dir = %v, want 1 file", moved)
	}
}

func TestRun_inbox_emptyDir(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"meta":{},"entries":[]}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	inboxDir := filepath.Join(dir, "inbox")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"inbox", "--catalog", catalogPath, "--inbox", inboxDir}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do':\n%s", out.String())
	}
}

func TestRun_inbox_flagsRequired(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"inbox", "--catalog", "/x.json"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit when --inbox is missing")
	}
}
