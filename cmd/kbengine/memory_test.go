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
	code, _, e := runMem(t, "add", "--name", "likes-short", "--topic", "стиль", "--description", "любит коротко", "--body", "текст", dir)
	if code != 0 {
		t.Fatalf("add: %d %s", code, e)
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
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("ручной горячий слой"), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestMemory_addKeepsTopicListFresh(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	runMem(t, "add", "--name", "b", "--topic", "здоровье", "--description", "про b", dir)
	hot, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if !strings.Contains(string(hot), "- здоровье\n- финансы") {
		t.Fatalf("перечень тем не собран:\n%s", hot)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("check: %d %s", code, out)
	}
}

// Запись положили мимо add: перечень отстал, check называет это, topics чинит.
func TestMemory_topicsFixesStaleList(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	hand := "---\nname: b\ntopic: здоровье\ndescription: про b\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "cold", "b.md"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "cold", "INDEX.md")
	f, _ := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("- [b](b.md) — про b\n")
	f.Close()
	if code, out, _ := runMem(t, "check", dir); code != 1 || !strings.Contains(out, "stale-topics") {
		t.Fatalf("check: %d %q", code, out)
	}
	if code, _, e := runMem(t, "topics", dir); code != 0 {
		t.Fatalf("topics: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после topics check красный: %d %s", code, out)
	}
}

func TestMemory_topicsWithoutMarkersLeavesHotAlone(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("ручной"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runMem(t, "topics", dir); code != 1 {
		t.Errorf("topics без меток: code=%d, want 1", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "HOT.md")); string(b) != "ручной" {
		t.Errorf("HOT.md затронут: %q", b)
	}
}

func TestMemory_agentQueueEndToEnd(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	code, out, e := runMem(t, "add", "--origin", "agent", "--name", "agent-note", "--topic", "стиль", "--description", "предложение", "--body", "тело", dir)
	if code != 0 || !strings.Contains(out, "ждет одобрения") {
		t.Fatalf("add --origin agent: %d %q %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "cold", "agent-note.md")); err == nil {
		t.Fatal("агент записал прямо в холодный слой")
	}
	if code, out, _ := runMem(t, "pending", dir); code != 0 || !strings.Contains(out, "agent-note") {
		t.Fatalf("pending: %d %q", code, out)
	}
	if code, _, e := runMem(t, "approve", "--name", "agent-note", "--to", "cold", dir); code != 0 {
		t.Fatalf("approve: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после approve check красный: %d %s", code, out)
	}
	if code, out, _ := runMem(t, "pending", dir); !strings.Contains(out, "очередь пуста") || code != 0 {
		t.Errorf("pending после approve: %d %q", code, out)
	}
}

func TestMemory_approveToHotAndReject(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--origin", "agent", "--name", "rule-1", "--topic", "стиль", "--description", "не пушить в main", dir)
	runMem(t, "add", "--origin", "agent", "--name", "rule-2", "--topic", "стиль", "--description", "лишнее", dir)
	if code, _, e := runMem(t, "approve", "--name", "rule-1", "--to", "hot", dir); code != 0 {
		t.Fatalf("approve hot: %d %s", code, e)
	}
	hot, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if !strings.Contains(string(hot), "- не пушить в main") {
		t.Errorf("правила нет в HOT.md:\n%s", hot)
	}
	if code, _, e := runMem(t, "reject", "--name", "rule-2", dir); code != 0 {
		t.Fatalf("reject: %d %s", code, e)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "journal", "*rule-*"))
	if len(left) != 2 {
		t.Errorf("в журнале должно быть 2 файла (одобренное и отклоненное), есть %v", left)
	}
	if code, _, _ := runMem(t, "approve", "--name", "rule-1", "--to", "hot", dir); code != 1 {
		t.Errorf("повторное одобрение: code=%d, want 1", code)
	}
	if code, _, _ := runMem(t, "approve", "--name", "x", "--to", "middle", dir); code != 2 {
		t.Errorf("--to middle: code=%d, want 2", code)
	}
}
