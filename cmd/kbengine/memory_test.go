package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
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
	code, _, e := runMem(t, "add", "--name", "likes-short", "--topic", "стиль", "--description", "любит коротко", "--body", "текст", dir)
	if code != 0 {
		t.Fatalf("add: %d %s", code, e)
	}
	hot := "# Горячая\n" + domain.TopicsStart + "\n- стиль\n" + domain.TopicsEnd + "\n"
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(hot), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("check после add: %d %s", code, out)
	}
	if code, _, _ := runMem(t, "add", "--name", "Bad Name", "--topic", "стиль", "--description", "x", dir); code != 2 {
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

// Без папки в аргументах память живет в dvk-memory/ рядом с настройками окна.
func TestMemory_defaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code, _, e := runMem(t, "init"); code != 0 {
		t.Fatalf("init без папки: %d %s", code, e)
	}
	def := filepath.Join(home, "Library", "Application Support", "kb-desktop", "dvk-memory")
	for _, f := range []string{"HOT.md", "cold/INDEX.md", "journal"} {
		if _, err := os.Stat(filepath.Join(def, f)); err != nil {
			t.Errorf("нет %s: %v", f, err)
		}
	}
	if code, out, e := runMem(t, "check"); code != 0 {
		t.Errorf("check без папки: %d %s %s", code, out, e)
	}
}

func TestMemory_addNeedsTopic(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if code, _, _ := runMem(t, "add", "--name", "x", "--description", "y", dir); code != 2 {
		t.Errorf("add без --topic: code=%d, want 2", code)
	}
}

// Запись есть, перечня тем в горячем слое нет: check обязан назвать это.
func TestMemory_checkNamesMissingTopicList(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "x", "--topic", "стиль", "--description", "y", dir)
	code, out, _ := runMem(t, "check", dir)
	if code != 1 || !strings.Contains(out, "no-topic-list") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestMemory_checkIndexLimitFlag(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if code, out, _ := runMem(t, "check", "--index-limit", "5", dir); code != 1 || !strings.Contains(out, "index-too-big") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
