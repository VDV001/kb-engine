package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func emptyCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries":[],"last_updated":"2026-08-01"}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func entriesOf(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc.Entries
}

func TestAdd_writesAnOwnArtefact(t *testing.T) {
	path := emptyCatalog(t)
	var out, errOut bytes.Buffer

	code := run([]string{"add", "--catalog", path,
		"--title", "Harness Engineering Defaults",
		"--category", "standards",
		"--file", "standards/harness-engineering-defaults/v1.md",
		"--version", "1.3.0", "--lifecycle", "canonical",
		"--tags", "internal-asset,harness", "--description", "11 правил обвязки",
	}, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	entries := entriesOf(t, path)
	if len(entries) != 1 {
		t.Fatalf("записей = %d, want 1", len(entries))
	}
	e := entries[0]
	for field, want := range map[string]any{
		"file":      "standards/harness-engineering-defaults/v1.md",
		"version":   "1.3.0",
		"lifecycle": "canonical",
		"category":  "standards",
		"status":    "read",
		"source":    "internal",
	} {
		if got := e[field]; got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
	if !strings.Contains(out.String(), "added id=") {
		t.Errorf("не сказано, что добавлено: %s", out.String())
	}
}

// Тот же файл дважды — не ошибка и не успех: сказать об этом лучше, чем молча
// вернуть ноль или упасть.
func TestAdd_refusesToAddTheSameFileTwice(t *testing.T) {
	path := emptyCatalog(t)
	args := []string{"add", "--catalog", path, "--title", "Стандарт",
		"--category", "standards", "--file", "standards/x/v1.md"}

	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("первый прогон: exit = %d, %s", code, errOut.String())
	}
	out.Reset()
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("второй прогон: exit = %d, %s", code, errOut.String())
	}

	if n := len(entriesOf(t, path)); n != 1 {
		t.Errorf("записей = %d, want 1 — файл добавился дважды", n)
	}
	if !strings.Contains(out.String(), "already in the catalog") {
		t.Errorf("повтор прошёл молча: %s", out.String())
	}
}

func TestAdd_rejectsBadInput(t *testing.T) {
	path := emptyCatalog(t)
	base := []string{"add", "--catalog", path, "--title", "X", "--category", "standards", "--file", "standards/x/v1.md"}

	for _, tc := range []struct {
		name, flag, value, wantIn string
	}{
		{"неизвестная категория", "--category", "не-такая", "category"},
		{"путь наружу", "--file", "../../etc/passwd", "leaves the knowledge base"},
		{"битый семвер", "--version", "1.3", "version"},
		{"неизвестное состояние", "--lifecycle", "живая", "lifecycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, base...), tc.flag, tc.value)
			var out, errOut bytes.Buffer
			if code := run(args, &out, &errOut); code == 0 {
				t.Fatalf("ожидался ненулевой код выхода")
			}
			if !strings.Contains(errOut.String(), tc.wantIn) {
				t.Errorf("причина не названа: %s", errOut.String())
			}
			if n := len(entriesOf(t, path)); n != 0 {
				t.Errorf("записей = %d — при отказе ничего не должно записаться", n)
			}
		})
	}
}

func TestAdd_requiresTheEssentialFlags(t *testing.T) {
	for _, missing := range []string{"--catalog", "--title", "--category", "--file"} {
		t.Run(missing, func(t *testing.T) {
			args := []string{"add", "--catalog", emptyCatalog(t), "--title", "X",
				"--category", "standards", "--file", "standards/x/v1.md"}
			for i, a := range args {
				if a == missing {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			var out, errOut bytes.Buffer
			if code := run(args, &out, &errOut); code == 0 {
				t.Fatalf("без %s команда отработала успешно", missing)
			}
			if !strings.Contains(errOut.String(), missing) {
				t.Errorf("не названо, какого флага не хватает: %s", errOut.String())
			}
		})
	}
}
