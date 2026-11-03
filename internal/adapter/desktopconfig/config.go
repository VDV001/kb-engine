// Package desktopconfig reads and writes the settings of the kb-desktop window:
// which folders are indexed, which are skipped, where the catalog lives.
//
// Живет вне desktop/, потому что тот же файл читает kbengine-mcp: папки,
// выбранные в окне, — это папки, по которым ищет агент. Две копии разбора
// разошлись бы на первом же «~» в пути.
package desktopconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config — настройки приложения. Лежат в ~/Library/Application Support/
// kb-desktop/config.json, а не в репозитории: там пути к личным папкам.
type Config struct {
	// Roots — папки, которые индексируются для поиска по файлам.
	Roots []string `json:"roots"`
	// Exclude — имена папок, в которые обход не заходит (вдобавок к .git,
	// node_modules и т.п., которые адаптер пропускает всегда).
	Exclude []string `json:"exclude"`
	// Catalog — catalog.json базы знаний; пусто — вкладки базы нет.
	Catalog string `json:"catalog"`
	// Server — адрес удаленного kbengine serve. Пока не используется: окно
	// работает с локальным движком, удаленный источник — этап 5 плана.
	Server string `json:"server"`
}

// Path is where the window keeps its config.
func Path(home string) string {
	return filepath.Join(home, "Library", "Application Support", "kb-desktop", "config.json")
}

// IndexPath is the file index snapshot next to the config: окно и MCP делят
// один снимок, и второй из них стартует с инкремента, а не с полной сборки.
func IndexPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "index.gob")
}

// Default is the config written on the window's first run.
func Default(home string) Config {
	cowork := filepath.Join(home, "claude-cowork")
	return Config{
		Roots:   []string{cowork},
		Exclude: []string{"finances"},
		Catalog: filepath.Join(cowork, "knowledge-base", "_data", "catalog.json"),
	}
}

// Read reads an existing config. A missing file is fs.ErrNotExist: читателю
// вроде MCP-сервера умолчания не положены — их выбирает окно, а не он.
func Read(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Load reads the config, writing the defaults on first run so the person
// has a file to edit. A file that exists but does not parse is an error.
func Load(path, home string) (Config, error) {
	cfg, err := Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg = Default(home)
		return cfg, Write(path, cfg)
	}
	return cfg, err
}

// Write saves the config with owner-only permissions.
func Write(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// NormalizeRoots раскрывает «~», оставляет только существующие абсолютные папки
// и называет остальные. Удаленная папка или опечатка в конфиге не роняют окно:
// раньше любая такая строка доходила до log.Fatal (ревью #371).
func NormalizeRoots(roots []string, home string) (ok, skipped []string) {
	for _, r := range roots {
		p := r
		if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		if !filepath.IsAbs(p) {
			skipped = append(skipped, r+" (путь не абсолютный)")
			continue
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			skipped = append(skipped, r+" (папки нет)")
			continue
		}
		if !readable(p) {
			skipped = append(skipped, r+" (нет доступа: macOS не дает читать папку)")
			continue
		}
		ok = append(ok, filepath.Clean(p))
	}
	return ok, skipped
}

// readable reports whether a folder's listing can be read. os.Stat проходит и
// для ~/Documents без разрешения macOS, а обход потом получает EPERM; поэтому
// проверяется само чтение, до записи в конфиг.
func readable(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}
