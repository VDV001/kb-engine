package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func TestConfig_ValidateModules(t *testing.T) {
	tests := []struct {
		name    string
		modules []desktopconfig.Module
		want    error
	}{
		{"нет модулей", nil, nil},
		{"финансы", []desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}, nil},
		// Модуль от более новой сборки не роняет прежнюю: хранится, вкладки нет.
		{"неизвестный терпится", []desktopconfig.Module{{ID: "chat", Enabled: true}}, nil},
		{"пустой id", []desktopconfig.Module{{ID: "", Enabled: true}}, desktopconfig.ErrModuleID},
		{"повтор", []desktopconfig.Module{{ID: desktopconfig.ModuleFinances}, {ID: desktopconfig.ModuleFinances, Enabled: true}}, desktopconfig.ErrModuleID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := desktopconfig.Config{Modules: tt.modules}.Validate()
			if tt.want == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
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

// Испорченный список модулей — отказ при чтении, как у источников.
func TestRead_DuplicateModuleIsAnError(t *testing.T) {
	path := writeRaw(t, `{"sources":[],"modules":[{"id":"finances","enabled":true},{"id":"finances","enabled":false}]}`)
	if _, err := desktopconfig.Read(path); !errors.Is(err, desktopconfig.ErrModuleID) {
		t.Fatalf("err = %v, want ErrModuleID", err)
	}
}
