package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Протокол помощника: «-» и вид файла, иначе отказ с кодом 2 до копии файла
// — вид идет в имя временного файла и не должен нести путь.
func TestRunRejectsBadArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"x", "pdf"}, {"-", "../evil"}, {"-", "PDF"}, {"-", "pdf", "more"}} {
		var errOut bytes.Buffer
		if code := run(args, strings.NewReader("x"), &bytes.Buffer{}, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage") {
			t.Fatalf("%q: код %d, %q", args, code, errOut.String())
		}
	}
}

// Скрипт распознавания вшит в помощник и читает путь из переменной.
func TestScriptEmbedded(t *testing.T) {
	if !strings.Contains(script, "Windows.Media.Ocr") || !strings.Contains(script, "$env:KB_FILE") {
		t.Fatal("ocr.ps1 не вшит")
	}
}

// Копия, брошенная снятым помощником, стирается следующим; свежая — чужая
// текущая — остается (ревью #637).
func TestSweepStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old, fresh, other := filepath.Join(dir, "kb-extract-1.pdf"), filepath.Join(dir, "kb-extract-2.pdf"), filepath.Join(dir, "чужое.pdf")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{old, other} {
		if err := os.Chtimes(p, now.Add(-2*staleAfter), now.Add(-2*staleAfter)); err != nil {
			t.Fatal(err)
		}
	}
	sweepStale(dir, now)
	for p, want := range map[string]bool{old: false, fresh: true, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: есть %t, want %t", filepath.Base(p), err == nil, want)
		}
	}
}
