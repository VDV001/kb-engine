package main

import "testing"

// Сбой скрипта Hello (код 1 от PowerShell) ведет к паролю учетной записи, а не
// к отказу; отказ человека — свой код (ревью #637).
func TestHelloAction(t *testing.T) {
	for code, want := range map[int]helloStep{0: helloOK, helloExitDenied: helloDenied, 3: helloPassword, 1: helloPassword, -1: helloPassword} {
		if got := helloAction(code); got != want {
			t.Errorf("код %d: %v, want %v", code, got, want)
		}
	}
}

// Проводник выделяет файл и в пути с пробелами и запятой: кавычки — вокруг пути.
func TestExplorerSelect(t *testing.T) {
	for path, want := range map[string]string{
		`C:\a\f.pdf`:               `explorer /select,"C:\a\f.pdf"`,
		`C:\My Documents\д, 1.pdf`: `explorer /select,"C:\My Documents\д, 1.pdf"`,
	} {
		if got := explorerSelect(path); got != want {
			t.Errorf("%q: %q, want %q", path, got, want)
		}
	}
}
