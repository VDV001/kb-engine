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
	for _, f := range []string{"HOT.md", "cold/INDEX.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("нет %s: %v", f, err)
		}
	}
	ps, err := memory.New(st, 0).Check()
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

func TestAdd_writesNoteAndIndexLine(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	n, _ := domain.NewColdNote("owner-likes-short", "владелец любит короткие ответы")
	if err := st.Add(n, "Тело записи."); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Notes) != 1 || snap.Notes[0].Description != "владелец любит короткие ответы" || snap.Notes[0].Name != "owner-likes-short" {
		t.Errorf("notes = %+v", snap.Notes)
	}
	if len(snap.Index) != 1 || snap.Index[0].File != "owner-likes-short.md" || snap.Index[0].Hook != "владелец любит короткие ответы" {
		t.Errorf("index = %+v", snap.Index)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "cold", "owner-likes-short.md"))
	if !strings.Contains(string(body), "Тело записи.") {
		t.Errorf("тела нет в файле: %q", body)
	}
	if ps, _ := memory.New(st, 0).Check(); len(ps) != 0 {
		t.Errorf("после add проверка красная: %v", ps)
	}
}

func TestAdd_refusesDuplicate(t *testing.T) {
	st := memorydir.New(t.TempDir())
	_ = st.Init()
	n, _ := domain.NewColdNote("dup", "первая")
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
	hand := "---\nname: handmade\ndescription: положил руками\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "cold", "handmade.md"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := memory.New(st, 0).Check()
	if err != nil || len(ps) != 1 || ps[0].Kind != domain.ProblemNotIndexed {
		t.Fatalf("ps=%v err=%v", ps, err)
	}
}
