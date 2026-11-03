package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

type fakeReader struct{}

func (fakeReader) Read(path string) (string, error) {
	if path == "/w/ok.md" {
		return "text", nil
	}
	return "", filesearch.ErrNotAllowed
}

func TestApp_OpenAndRevealCheckThePathFirst(t *testing.T) {
	var calls [][]string
	a := &App{files: fakeReader{}, run: func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}}

	if err := a.Open("/w/ok.md"); err != nil {
		t.Fatal(err)
	}
	if err := a.Reveal("/w/ok.md"); err != nil {
		t.Fatal(err)
	}
	// Запрещенный путь не доходит до open вовсе.
	if err := a.Open("/w/.env"); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Fatalf("Open(.env) err = %v", err)
	}
	if err := a.Reveal("/etc/passwd"); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Fatalf("Reveal(passwd) err = %v", err)
	}
	want := [][]string{{"open", "/w/ok.md"}, {"open", "-R", "/w/ok.md"}}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}
