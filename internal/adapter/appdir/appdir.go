// Package appdir is where the DVK window keeps its files: настройки, доступы,
// память. Одно место для всех писателей: на Маке ~/Library/Application
// Support/kb-desktop, на Windows — %APPDATA%\kb-desktop (#622, этап 5).
package appdir

import (
	"path/filepath"
	"runtime"
)

// Dir returns the window's folder under home: home подменяют тесты, поэтому
// путь строится от него, а не от os.UserConfigDir.
func Dir(home string) string { return dirFor(runtime.GOOS, home) }

func dirFor(goos, home string) string {
	if goos == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "kb-desktop")
	}
	return filepath.Join(home, "Library", "Application Support", "kb-desktop")
}
