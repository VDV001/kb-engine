package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
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
	cfg, err := desktopconfig.Load(path, home)
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
	if !slices.Equal(got.roots, wantRoots) || !slices.Equal(got.exclude, []string{"finances", "archive"}) {
		t.Fatalf("apply(%q, %q)", got.roots, got.exclude)
	}
	// Записано на диск: следующий запуск окна увидит те же папки.
	saved, err := desktopconfig.Load(s.path, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.Roots, wantRoots) || !slices.Equal(saved.Exclude, []string{"finances", "archive"}) {
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

func TestFolderSettings_NamedErrors(t *testing.T) {
	s, _, _ := newTestSettings(t)
	if err := s.Set(nil, nil); !errors.Is(err, errNoFolders) {
		t.Errorf("пустой список: %v", err)
	}
	if err := s.Set([]string{"~/gone"}, nil); !errors.Is(err, errBadFolder) {
		t.Errorf("нет папки: %v", err)
	}
}

func TestFolderSettings_UnreadableFolderIsRejectedBeforeSaving(t *testing.T) {
	// Папка под защитой macOS (~/Documents без доступа) проходила os.Stat,
	// сохранялась, а потом не открывалась — и следующий запуск окна падал.
	s, got, home := newTestSettings(t)
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	before, _ := os.ReadFile(s.path)
	if err := s.Set([]string{"~/locked"}, nil); !errors.Is(err, errBadFolder) || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(s.path); string(before) != string(after) || got.calls != 0 {
		t.Fatal("отказ изменил конфиг или индекс")
	}
}

func TestFolderSettings_ApplyFailureDoesNotSave(t *testing.T) {
	s, _, _ := newTestSettings(t)
	s.apply = func([]string, []string) error { return errors.New("open root: permission denied") }
	before, _ := os.ReadFile(s.path)
	if err := s.Set([]string{"~/notes"}, nil); err == nil {
		t.Fatal("ждали ошибку применения")
	}
	if after, _ := os.ReadFile(s.path); string(before) != string(after) {
		t.Fatal("конфиг записан, хотя папки не открылись")
	}
}

func TestFolderSettings_RejectsNestedFolders(t *testing.T) {
	s, _, home := newTestSettings(t)
	if err := os.MkdirAll(filepath.Join(home, "notes", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := s.Set([]string{"~/notes", "~/notes/inner"}, nil)
	if !errors.Is(err, errBadFolder) || !strings.Contains(err.Error(), "inner") {
		t.Fatalf("вложенная папка: %v", err)
	}
}

func TestFolderSettings_ExcludesDedupeIgnoringCase(t *testing.T) {
	s, got, _ := newTestSettings(t)
	if err := s.Set([]string{"~/notes"}, []string{"finances", "Finances", " FINANCES "}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.exclude, []string{"finances"}) {
		t.Fatalf("exclude = %q", got.exclude)
	}
}

func TestFolderSettings_GetSplitsMissingFolders(t *testing.T) {
	// Удаленная папка из конфига не показывается рабочей: она отдельно, чтобы
	// сохранение не отказывало из-за папки, которую человек не добавлял.
	s, _, home := newTestSettings(t)
	s.cfg.SetFolderRoots([]string{filepath.Join(home, "gone"), filepath.Join(home, "notes")})
	v := s.Get()
	if !slices.Equal(v.Roots, []string{filepath.Join(home, "notes")}) || len(v.Missing) != 1 {
		t.Fatalf("Get = %+v", v)
	}
}
