package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

type fakeReader struct{}

func (fakeReader) Check(path string) error {
	if path == "/w/ok.md" {
		return nil
	}
	return filesearch.ErrNotAllowed
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

func TestApp_PreviewChecksThePathAndDoesNotWaitForTheWindow(t *testing.T) {
	started, release := make(chan []string, 1), make(chan struct{})
	a := &App{files: fakeReader{}, run: func(name string, args ...string) error {
		started <- append([]string{name}, args...)
		<-release // qlmanage -p держит окно, пока его не закроют
		return nil
	}}
	defer close(release)
	if err := a.Preview("/w/ok.md"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if !slices.Equal(got, []string{"qlmanage", "-p", "/w/ok.md"}) {
			t.Errorf("вызов %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("быстрый просмотр не запущен")
	}
	if err := a.Preview("/w/.env"); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Errorf("запрещенный путь дошел до просмотра: %v", err)
	}
}

func TestApp_VersionMethodsWithoutTheDeskAreRefusedNotPanics(t *testing.T) {
	a := &App{}
	if err := a.VersionMark("/a", "x"); err == nil {
		t.Error("VersionMark без окна версий")
	}
	if err := a.VersionCurrent("/a", true); err == nil {
		t.Error("VersionCurrent без окна версий")
	}
	if _, err := a.VersionCompare("/a", "/b"); err == nil {
		t.Error("VersionCompare без окна версий")
	}
}

func TestApp_PreviewReportsAnImmediateFailure(t *testing.T) {
	a := &App{files: fakeReader{}, run: func(string, ...string) error { return errors.New("qlmanage: not found") }}
	if err := a.Preview("/w/ok.md"); err == nil {
		t.Error("мгновенный сбой просмотра проглочен: кнопка молча не работает")
	}
}
