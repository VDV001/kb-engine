package main

import (
	"bytes"
	"strings"
	"testing"
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
