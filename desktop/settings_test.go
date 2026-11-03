package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type applied struct {
	roots, exclude []string
	calls          int
}

func newTestSettings(t *testing.T) (*folderSettings, *applied, string) {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"notes", "work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home, "cfg", "config.json")
	cfg, err := loadConfig(path, home)
	if err != nil {
		t.Fatal(err)
	}
	got := &applied{}
	s := &folderSettings{path: path, home: home, cfg: cfg, apply: func(roots, exclude []string) error {
		got.roots, got.exclude = roots, exclude
		got.calls++
		return nil
	}}
	return s, got, home
}

func TestFolderSettings_SetSavesAndApplies(t *testing.T) {
	s, got, home := newTestSettings(t)
	err := s.Set([]string{"~/notes", filepath.Join(home, "work"), "~/notes/"}, []string{" finances ", "", "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	wantRoots := []string{filepath.Join(home, "notes"), filepath.Join(home, "work")}
	if !slices.Equal(got.roots, wantRoots) || !slices.Equal(got.exclude, []string{"finances", "Archive"}) {
		t.Fatalf("apply(%q, %q)", got.roots, got.exclude)
	}
	// Записано на диск: следующий запуск окна увидит те же папки.
	saved, err := loadConfig(s.path, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.Roots, wantRoots) || !slices.Equal(saved.Exclude, []string{"finances", "Archive"}) {
		t.Fatalf("saved = %+v", saved)
	}
	if v := s.Get(); !slices.Equal(v.Roots, wantRoots) {
		t.Fatalf("Get = %+v", v)
	}
}

func TestFolderSettings_RejectsBadInputWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roots []string
		want  string
	}{
		{"нет такой папки", []string{"~/gone"}, "gone"},
		{"относительный путь", []string{"notes"}, "notes"},
		{"пустой список", nil, "хотя бы одна"},
		{"файл вместо папки", []string{"~/cfg/config.json"}, "config.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, got, home := newTestSettings(t)
			before, _ := os.ReadFile(s.path)
			err := s.Set(tc.roots, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
			// Отказ ничего не меняет: ни файла, ни индекса.
			after, _ := os.ReadFile(s.path)
			if string(before) != string(after) || got.calls != 0 {
				t.Fatalf("отказ изменил состояние: calls=%d", got.calls)
			}
			_ = home
		})
	}
}
