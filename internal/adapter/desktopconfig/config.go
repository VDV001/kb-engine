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
)

// Config — настройки приложения. Лежат в ~/Library/Application Support/
// kb-desktop/config.json, а не в репозитории: там пути к личным папкам.
type Config struct {
	// Sources — откуда окно и MCP берут записи и файлы; порядок — приоритет.
	Sources []Source `json:"sources"`
	// Roots — включенные папки из Sources. Пишется производным при каждой
	// записи для kbengine-mcp прежней сборки, читается только при переезде
	// конфига без sources.
	Roots []string `json:"roots"`
	// Exclude — имена папок, в которые обход не заходит (вдобавок к .git,
	// node_modules и т.п., которые адаптер пропускает всегда).
	Exclude []string `json:"exclude"`
	// Catalog — первый включенный каталог из Sources, производный, как Roots.
	Catalog string `json:"catalog"`
	// Server — адрес сервера из конфига до источников; читается только при
	// переезде, дальше сервер живет источником вида server.
	Server string `json:"server,omitempty"`
	// Modules — разделы окна (финансы и дальше) с признаком, включен ли
	// каждый; в списке и выключенные, и неизвестные этой сборке. Включен ли —
	// только через ModuleOn. Порядок — порядок вкладок.
	Modules []Module `json:"modules,omitempty"`
	// Tabs — вкладки поиска после «Все», по порядку. nil — не настраивались
	// (TabList отдает DefaultTabs), пустой список — только «Все». Без
	// omitempty: пустой список иначе не записался бы и вернулся бы набором.
	Tabs []Tab `json:"tabs"`
	// Accents — оформление окна: какие акценты спектра включены. Нулевое —
	// свой набор без выключенных, то есть все.
	Accents Accents `json:"accents"`
	// Connections — доступы окна (Google и дальше), без секретов: они в Связке.
	Connections []Connection `json:"connections,omitempty"`
	// Chat — модель чата (#436): адрес, имя, режим. Ключ — в Связке ключей.
	Chat Chat `json:"chat"`
	// Spend — лимит месяца и свои цены моделей для «Денег» статистики (#493).
	Spend Spend `json:"spend"`
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

// ExtractDir is where the window keeps text recognized from PDFs and images,
// next to the config: окно пишет, MCP читает тот же каталог.
func ExtractDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "extract")
}

// Default is the config written on the window's first run.
func Default(home string) Config {
	cowork := filepath.Join(home, "claude-cowork")
	cfg := Config{
		Roots:   []string{cowork},
		Exclude: []string{"finances"},
		Catalog: filepath.Join(cowork, "knowledge-base", "_data", "catalog.json"),
	}
	cfg.migrate()
	return cfg
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
	cfg.migrate()
	cfg.dropBadModules()
	cfg.dropBadTabs()
	cfg.dropBadAccents()
	cfg.dropBadSpend()
	if err := cfg.Validate(); err != nil {
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
	// Конфиг прежней формы сначала переезжает, как при чтении: иначе сервер из
	// него пропал бы (sources: null), а roots и catalog выжили бы случайно.
	cfg.migrate()
	cfg.dropBadModules()
	cfg.dropBadTabs()
	cfg.dropBadAccents()
	cfg.dropBadSpend()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.Roots, cfg.Catalog, cfg.Server = cfg.FolderRoots(), cfg.CatalogPath(), ""
	if cfg.Roots == nil {
		cfg.Roots = []string{}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Через временный файл и rename: MCP-сервер, другой процесс, читает конфиг
	// и без замка (search_files) и не должен видеть его обрезанным.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp") // CreateTemp создает с 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после rename файла уже нет
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// NormalizeRoots раскрывает «~», оставляет только существующие абсолютные папки
// и называет остальные. Удаленная папка или опечатка в конфиге не роняют окно:
// раньше любая такая строка доходила до log.Fatal (ревью #371).
func NormalizeRoots(roots []string, home string) (ok, skipped []string) {
	for _, r := range roots {
		p := ExpandHome(r, home)
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
		ok = append(ok, p)
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
