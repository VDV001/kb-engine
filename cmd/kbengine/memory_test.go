package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMem(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"memory"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestMemory_initThenCheckIsGreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	if code, _, e := runMem(t, "init", dir); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if code, out, e := runMem(t, "check", dir); code != 0 {
		t.Fatalf("check: %d %s %s", code, out, e)
	}
	if code, _, _ := runMem(t, "init", dir); code == 0 {
		t.Error("повторный init затер бы память, а вернул 0")
	}
}

func TestMemory_checkNamesTheProblem(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(strings.Repeat("я", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runMem(t, "check", dir)
	if code != 1 || !strings.Contains(out, "hot-too-big") || !strings.Contains(out, "5000") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// свой лимит
	if code, _, _ := runMem(t, "check", "--hot-limit", "6000", dir); code != 0 {
		t.Errorf("--hot-limit 6000 должен пускать 5000 знаков, code=%d", code)
	}
}

func TestMemory_addThenCheck(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	code, _, e := runMem(t, "add", "--name", "likes-short", "--description", "любит коротко", "--body", "текст", dir)
	if code != 0 {
		t.Fatalf("add: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("check после add: %d %s", code, out)
	}
	if code, _, _ := runMem(t, "add", "--name", "Bad Name", "--description", "x", dir); code != 2 {
		t.Errorf("плохое имя: code=%d, want 2", code)
	}
}

func TestMemory_usage(t *testing.T) {
	if code, _, _ := runMem(t); code != 2 {
		t.Errorf("без подкоманды code=%d, want 2", code)
	}
	if code, _, _ := runMem(t, "nope"); code != 2 {
		t.Errorf("неизвестная подкоманда code=%d, want 2", code)
	}
}
