package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesSearch(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"notes/plan.md":  "заголовок\nПодписали договора с поставщиком",
		"notes/other.md": "про другое",
		".env":           "договор=секрет",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if code := run([]string{"files", "--root", root, "--q", "договор"}, &out, &errb); code != 0 {
		t.Fatalf("files вернул %d: %s", code, out.String()+errb.String())
	}
	said := out.String()
	for _, want := range []string{"plan.md", "Подписали договора", "2 files"} {
		if !strings.Contains(said, want) {
			t.Errorf("нет %q в выводе:\n%s", want, said)
		}
	}
	// Секрет не только не найден — его нет в индексе вообще (2 файла, а не 3).
	if strings.Contains(said, ".env") || strings.Contains(said, "секрет") {
		t.Errorf("секрет в выдаче:\n%s", said)
	}
}

func TestFilesNeedsRootAndQuery(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"files", "--q", "x"}, &out, &errb); code != 2 {
		t.Fatalf("без --root ждем 2, получили %d", code)
	}
	if code := run([]string{"files", "--root", t.TempDir()}, &out, &errb); code != 2 {
		t.Fatalf("без --q ждем 2, получили %d", code)
	}
}
