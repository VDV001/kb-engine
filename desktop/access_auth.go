//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"time"
)

// Touch ID и буфер на Маке — помощник kb-auth рядом с окном (auth/main.swift).
// Под Windows — access_auth_windows.go.

// authNextTo finds kb-auth next to the window; нет — nil (окно не из сборки).
func authNextTo(exe string) authHelper {
	bin := filepath.Join(filepath.Dir(exe), "kb-auth")
	if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return kbAuth{bin: bin, wait: 10 * time.Second}
	}
	return nil
}
