package memorydir_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Скелет обязан проходить собственную проверку: иначе первая же `check`
// после `init` ругнется на то, что мы сами положили.
func TestInit_skeletonPassesItsOwnCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"HOT.md", "cold/INDEX.md", "journal"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("нет %s: %v", f, err)
		}
	}
	ps, err := memory.New(st, memory.Limits{}).Check()
	if err != nil || len(ps) != 0 {
		t.Errorf("скелет не проходит проверку: %v, %v", ps, err)
	}
}

func TestInit_refusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	hot := filepath.Join(dir, "HOT.md")
	if err := os.WriteFile(hot, []byte("мои правки"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); !errors.Is(err, domain.ErrMemoryExists) {
		t.Fatalf("err = %v, want ErrMemoryExists", err)
	}
	if b, _ := os.ReadFile(hot); string(b) != "мои правки" {
		t.Errorf("правки человека затерты: %q", b)
	}
}

// withTopicList кладет в горячий слой перечень тем, как это делает `memory topics`.
func withTopicList(t *testing.T, dir string) {
	t.Helper()
	hot := "# Горячая\n\n" + domain.TopicsStart + "\n- стиль\n" + domain.TopicsEnd + "\n"
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(hot), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAdd_writesNoteAndIndexLine(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	n, _ := domain.NewColdNote("owner-likes-short", "стиль", "владелец любит короткие ответы")
	if err := st.Add(n, "Тело записи."); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Notes) != 1 || snap.Notes[0].Description != "владелец любит короткие ответы" || snap.Notes[0].Name != "owner-likes-short" || snap.Notes[0].Topic != "стиль" {
		t.Errorf("notes = %+v", snap.Notes)
	}
	if len(snap.Index) != 1 || snap.Index[0].File != "owner-likes-short.md" || snap.Index[0].Hook != "владелец любит короткие ответы" {
		t.Errorf("index = %+v", snap.Index)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "cold", "owner-likes-short.md"))
	if !strings.Contains(string(body), "Тело записи.") {
		t.Errorf("тела нет в файле: %q", body)
	}
	withTopicList(t, dir)
	if ps, _ := memory.New(st, memory.Limits{}).Check(); len(ps) != 0 {
		t.Errorf("после add проверка красная: %v", ps)
	}
}

func TestAdd_refusesDuplicate(t *testing.T) {
	st := memorydir.New(t.TempDir())
	_ = st.Init()
	n, _ := domain.NewColdNote("dup", "стиль", "первая")
	if err := st.Add(n, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(n, "b"); !errors.Is(err, domain.ErrNoteExists) {
		t.Fatalf("err = %v, want ErrNoteExists", err)
	}
}

// Load должен видеть записи, которые человек или агент положил мимо `add`:
// именно их и ловит проверка.
func TestLoad_seesHandWrittenNoteMissingFromIndex(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	hand := "---\nname: handmade\ntopic: стиль\ndescription: положил руками\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "cold", "handmade.md"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	withTopicList(t, dir)
	ps, err := memory.New(st, memory.Limits{}).Check()
	if err != nil || len(ps) != 1 || ps[0].Kind != domain.ProblemNotIndexed {
		t.Fatalf("ps=%v err=%v", ps, err)
	}
}

func TestDefaultPath(t *testing.T) {
	got := memorydir.DefaultPath("/Users/x")
	want := "/Users/x/Library/Application Support/kb-desktop/dvk-memory"
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

// Журнал в обычный поиск и в холодные записи не попадает: файлы в journal/
// не читаются как записи и не требуют строк индекса.
func TestLoad_ignoresJournal(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := os.WriteFile(filepath.Join(dir, "journal", "2026-11-09.md"), []byte("хроника без шапки"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load()
	if err != nil || len(snap.Notes) != 0 {
		t.Fatalf("notes=%v err=%v", snap.Notes, err)
	}
}

func TestLoad_reportsIndexSize(t *testing.T) {
	st := memorydir.New(t.TempDir())
	_ = st.Init()
	n, _ := domain.NewColdNote("a", "стиль", "про a")
	_ = st.Add(n, "x")
	snap, _ := st.Load()
	if snap.IndexRunes == 0 {
		t.Error("IndexRunes не посчитан")
	}
}

func TestSaveHot_replacesAtomically(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := st.SaveHot("новый горячий\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if string(b) != "новый горячий\n" {
		t.Errorf("HOT.md = %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".hot-*"))
	if len(left) != 0 {
		t.Errorf("остались временные файлы: %v", left)
	}
	info, _ := os.Stat(filepath.Join(dir, "HOT.md"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права HOT.md = %v, want 0600", info.Mode().Perm())
	}
}
