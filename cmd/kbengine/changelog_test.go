package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunChangelog(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "CHANGELOG.md")
	out := filepath.Join(dir, "changelog.json")
	md := "## [1.2.0] — 2026-03-01\n> tag\n### Added\n- a thing\n"
	if err := os.WriteFile(in, []byte(md), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var so, se bytes.Buffer
	if code := runChangelog([]string{"--in", in, "--out", out}, &so, &se); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, se.String())
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc["current_version"] != "1.2.0" {
		t.Errorf("current_version = %v, want 1.2.0", doc["current_version"])
	}
}

func TestRunChangelog_flagsRequired(t *testing.T) {
	var so, se bytes.Buffer
	if code := runChangelog([]string{"--in", "x.md"}, &so, &se); code == 0 {
		t.Fatal("expected non-zero exit when --out missing")
	}
}

func TestRunChangelog_missingInput(t *testing.T) {
	var so, se bytes.Buffer
	if code := runChangelog([]string{"--in", "/no/such.md", "--out", "/tmp/x.json"}, &so, &se); code == 0 {
		t.Fatal("expected non-zero exit for missing input file")
	}
}
