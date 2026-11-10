package cdpbrowser

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeBrowsers подменяет список браузеров Мака и PATH на временную папку.
func fakeBrowsers(t *testing.T, list func(dir string) []Browser) string {
	t.Helper()
	dir := t.TempDir()
	old := macBrowsers
	macBrowsers = list(dir)
	t.Cleanup(func() { macBrowsers = old })
	t.Setenv("PATH", "")
	return dir
}

func mkExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledListsOnlyExistingBrowsersInOrder(t *testing.T) {
	var chrome, brave string
	fakeBrowsers(t, func(dir string) []Browser {
		chrome, brave = filepath.Join(dir, "chrome"), filepath.Join(dir, "brave")
		mkExec(t, chrome)
		mkExec(t, brave)
		return []Browser{
			{"Google Chrome", chrome},
			{"Microsoft Edge", filepath.Join(dir, "нет-такого")},
			{"Brave", brave},
			{"Папка", dir}, // каталог — не браузер
		}
	})
	got := Installed()
	want := []Browser{{"Google Chrome", chrome}, {"Brave", brave}}
	if !slices.Equal(got, want) {
		t.Fatalf("Installed() = %v, want %v", got, want)
	}
}

func TestInstalledAddsPathBrowsersWithoutDuplicates(t *testing.T) {
	dir := fakeBrowsers(t, func(string) []Browser { return nil })
	bin := filepath.Join(dir, "chromium")
	mkExec(t, bin)
	t.Setenv("PATH", dir)
	macBrowsers = []Browser{{"Chromium", bin}} // тот же файл виден и по PATH
	got := Installed()
	if want := []Browser{{"Chromium", bin}}; !slices.Equal(got, want) {
		t.Fatalf("Installed() = %v, want %v: один файл — одна запись", got, want)
	}
}

func TestFindBrowserIsFirstInstalled(t *testing.T) {
	var brave string
	fakeBrowsers(t, func(dir string) []Browser {
		brave = filepath.Join(dir, "brave")
		mkExec(t, brave)
		return []Browser{{"Google Chrome", filepath.Join(dir, "нет")}, {"Brave", brave}}
	})
	got, err := FindBrowser()
	if err != nil || got != brave {
		t.Fatalf("FindBrowser() = %q, %v; want %q", got, err, brave)
	}
}

func TestFindBrowserNoneInstalled(t *testing.T) {
	fakeBrowsers(t, func(string) []Browser { return nil })
	if _, err := FindBrowser(); err != ErrNoBrowser {
		t.Fatalf("err = %v, want ErrNoBrowser", err)
	}
}
