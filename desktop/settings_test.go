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

// Перенесено с панели папок на экран источников: те же отказы, теперь через
// SetSources.
func withFolder(path string) []desktopconfig.Source {
	return []desktopconfig.Source{src("", desktopconfig.KindFolder, path, true)}
}

func TestSetSources_RejectsBadFolderWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
	}{
		{"нет такой папки", "~/gone", "gone"},
		{"относительный путь", "notes", "notes"},
		{"файл вместо папки", "~/cfg/config.json", "config.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, got, _ := newSourcesSettings(t)
			before, _ := os.ReadFile(s.path)
			err := s.SetSources(withFolder(tc.path), nil)
			if !errors.Is(err, errBadFolder) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want errBadFolder with %q", err, tc.want)
			}
			// Отказ ничего не меняет: ни файла, ни индекса.
			if after, _ := os.ReadFile(s.path); string(before) != string(after) || got.calls != 0 {
				t.Fatalf("отказ изменил состояние: calls=%d", got.calls)
			}
		})
	}
}

func TestSetSources_UnreadableFolderIsRejectedBeforeSaving(t *testing.T) {
	// Папка под защитой macOS (~/Documents без доступа) проходила os.Stat,
	// сохранялась, а потом не открывалась — и следующий запуск окна падал.
	s, got, home := newSourcesSettings(t)
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	before, _ := os.ReadFile(s.path)
	if err := s.SetSources(withFolder("~/locked"), nil); !errors.Is(err, errBadFolder) || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(s.path); string(before) != string(after) || got.calls != 0 {
		t.Fatal("отказ изменил конфиг или индекс")
	}
}

func TestSetSources_ApplyFailureDoesNotSave(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	s.apply = func([]string, []string, string) error { return errors.New("open root: permission denied") }
	before, _ := os.ReadFile(s.path)
	if err := s.SetSources(withFolder("~/notes"), nil); err == nil {
		t.Fatal("ждали ошибку применения")
	}
	if after, _ := os.ReadFile(s.path); string(before) != string(after) {
		t.Fatal("конфиг записан, хотя папки не открылись")
	}
}

func TestSetSources_NestedFolderIsNamed(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	if err := os.MkdirAll(filepath.Join(home, "notes", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := s.SetSources(append(withFolder("~/notes"), src("", desktopconfig.KindFolder, "~/notes/inner", true)), nil)
	if !errors.Is(err, errBadFolder) || !strings.Contains(err.Error(), "inner") {
		t.Fatalf("вложенная папка: %v", err)
	}
}

func TestSetSources_ExcludesDedupeIgnoringCase(t *testing.T) {
	s, got, _ := newSourcesSettings(t)
	if err := s.SetSources(withFolder("~/notes"), []string{"finances", "Finances", " FINANCES ", ""}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.exclude, []string{"finances"}) {
		t.Fatalf("exclude = %q", got.exclude)
	}
}

// Папка, что уже сохранена, но сейчас пропала (сетевой диск без VPN), не
// мешает сохранить список: она остается «нет связи» и не индексируется.
// Умолчание ~/claude-cowork во временном доме тестов как раз отсутствует.
func TestSetSources_KnownMissingFolderIsKept(t *testing.T) {
	s, got, home := newSourcesSettings(t)
	gone := s.cfg.FolderRoots()[0]
	list := []desktopconfig.Source{
		src("folder-1", desktopconfig.KindFolder, gone, true),
		src("", desktopconfig.KindFolder, "~/notes", true),
	}
	if err := s.SetSources(list, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.roots, []string{filepath.Join(home, "notes")}) {
		t.Fatalf("apply roots = %q", got.roots)
	}
	saved, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.FolderRoots(); !slices.Equal(got, []string{gone, "~/notes"}) {
		t.Fatalf("saved = %q", got)
	}
}
