package desktopconfig_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Плохая запись модулей не роняет чтение: модули — только вкладки, а конфиг
// читают и окно, и kbengine-mcp. Пустой id и повтор отбрасываются, первая
// запись остается; неизвестный id (более новая сборка) хранится.
func TestRead_BadModulesAreDropped(t *testing.T) {
	path := writeRaw(t, `{"sources":[{"id":"folder-1","kind":"folder","path":"/a","enabled":true}],
		"modules":[{"id":"chat","enabled":true},{"id":"","enabled":true},{"id":"finances","enabled":true},{"id":"finances","enabled":false}]}`)
	cfg, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatalf("err = %v, want nil: модули не должны ронять чтение источников", err)
	}
	want := []desktopconfig.Module{{ID: "chat", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: true}}
	if !slices.Equal(cfg.Modules, want) {
		t.Fatalf("modules = %+v, want %+v", cfg.Modules, want)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("sources = %+v", cfg.Sources)
	}
}

func TestConfig_ModuleOn(t *testing.T) {
	cfg := desktopconfig.Config{Modules: []desktopconfig.Module{
		{ID: desktopconfig.ModuleFinances, Enabled: true},
		{ID: "chat", Enabled: false},
	}}
	if !cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Error("финансы включены, ModuleOn = false")
	}
	if cfg.ModuleOn("chat") {
		t.Error("выключенный модуль считается включенным")
	}
	// По умолчанию модулей нет: окно — только поиск, пока человек не включит.
	if (desktopconfig.Config{}).ModuleOn(desktopconfig.ModuleFinances) {
		t.Error("модуль без записи в конфиге включен")
	}
}

// Модули, и неизвестные тоже, переживают круг записи и чтения.
func TestWrite_ModulesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := desktopconfig.Config{Modules: []desktopconfig.Module{
		{ID: desktopconfig.ModuleFinances, Enabled: true},
		{ID: "chat", Enabled: false},
	}}
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Modules, cfg.Modules) {
		t.Fatalf("modules = %+v, want %+v", got.Modules, cfg.Modules)
	}
}
