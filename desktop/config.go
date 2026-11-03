package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// config — настройки приложения. Лежат в ~/Library/Application Support/
// kb-desktop/config.json, а не в репозитории: там пути к личным папкам.
type config struct {
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

func configPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "kb-desktop", "config.json")
}

func defaultConfig(home string) config {
	cowork := filepath.Join(home, "claude-cowork")
	return config{
		Roots:   []string{cowork},
		Exclude: []string{"finances"},
		Catalog: filepath.Join(cowork, "knowledge-base", "_data", "catalog.json"),
	}
}

// loadConfig reads the config, writing the defaults on first run so the person
// has a file to edit. A file that exists but does not parse is an error.
func loadConfig(path, home string) (config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg := defaultConfig(home)
		return cfg, writeConfig(path, cfg)
	}
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func writeConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
