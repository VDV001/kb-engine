package main

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Включение модуля сохраняется в config.json и не трогает индекс: модуль —
// вкладка, а не источник.
func TestSetModules_SavesWithoutReindex(t *testing.T) {
	s, applied, _ := newSourcesSettings(t)
	if got := s.Modules(); !slices.Equal(got, []desktopconfig.Module{{ID: desktopconfig.ModuleChat, Enabled: false}, {ID: desktopconfig.ModuleFinances, Enabled: false}, {ID: desktopconfig.ModuleStats, Enabled: false}, {ID: desktopconfig.ModuleAccess, Enabled: false}, {ID: desktopconfig.ModuleMemory, Enabled: false}, {ID: desktopconfig.ModuleBrowser, Enabled: false}, {ID: desktopconfig.ModuleHelp, Enabled: false}}) {
		t.Fatalf("Modules() по умолчанию = %+v", got)
	}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if applied.calls != 0 {
		t.Errorf("apply вызван %d раз, модуль не источник", applied.calls)
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Errorf("в файле модули = %+v", cfg.Modules)
	}
	if !s.Modules()[1].Enabled {
		t.Error("Modules() не видит включенный модуль")
	}
}

// Модуль от более новой сборки, которого это окно не показывает, переживает
// сохранение; источники сохранение модулей не трогает, и наоборот.
func TestSetModules_KeepsUnknownAndSources(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	s.cfg.Modules = []desktopconfig.Module{{ID: "calendar", Enabled: true}}
	sources := slices.Clone(s.cfg.Sources)
	next := []desktopconfig.Source{{ID: "folder-9", Kind: desktopconfig.KindFolder, Path: filepath.Join(home, "notes"), Enabled: true}}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ModuleOn("calendar") || !cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Errorf("модули = %+v", cfg.Modules)
	}
	if !slices.Equal(cfg.Sources, sources) {
		t.Errorf("источники изменились: %+v", cfg.Sources)
	}
	if err := s.SetSources(next, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = desktopconfig.Read(s.path); !cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Errorf("SetSources потерял модули: %+v", cfg.Modules)
	}
}

// Окно присылает только известные модули; чужой id — ErrUnknownModule, повтор —
// ErrModuleID; файл прежний.
func TestSetModules_Rejects(t *testing.T) {
	tests := []struct {
		name string
		list []desktopconfig.Module
		want error
	}{
		{"неизвестный", []desktopconfig.Module{{ID: "calendar", Enabled: true}}, desktopconfig.ErrUnknownModule},
		{"повтор", []desktopconfig.Module{{ID: desktopconfig.ModuleFinances}, {ID: desktopconfig.ModuleFinances, Enabled: true}}, desktopconfig.ErrModuleID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newSourcesSettings(t)
			if err := s.SetModules(tt.list); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if cfg, _ := desktopconfig.Read(s.path); len(cfg.Modules) != 0 {
				t.Errorf("записано %+v", cfg.Modules)
			}
		})
	}
}

// Порядок в конфиге — порядок вкладок: включение финансов не двигает модуль
// новой сборки, а модуль, которого окно не прислало, остается как был.
func TestSetModules_KeepsOrderAndOmitted(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	s.cfg.Modules = []desktopconfig.Module{{ID: "calendar", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: false}}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	want := []desktopconfig.Module{{ID: "calendar", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: true}}
	if cfg, _ := desktopconfig.Read(s.path); !slices.Equal(cfg.Modules, want) {
		t.Fatalf("modules = %+v, want %+v", cfg.Modules, want)
	}
	if err := s.SetModules(nil); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := desktopconfig.Read(s.path); !slices.Equal(cfg.Modules, want) {
		t.Fatalf("пустой список стер модули: %+v", cfg.Modules)
	}
	// Снимок для чтения без f.mu видит новое значение.
	if !s.snap.Load().cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Error("снимок настроек не обновлен")
	}
}

// Справка (решение владельца 07.11): шпаргалка команд и клавиш — модуль окна
// со своей вкладкой; данных на сервере у нее нет, включается как остальные.
func TestSetModules_Help(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleHelp, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(s.Modules(), func(m desktopconfig.Module) bool { return m.ID == desktopconfig.ModuleHelp })
	if i < 0 || !s.Modules()[i].Enabled {
		t.Fatalf("Modules() = %+v", s.Modules())
	}
	if err := s.SetModuleServer(desktopconfig.ModuleHelp, "http://127.0.0.1:8097"); !errors.Is(err, desktopconfig.ErrModuleServer) {
		t.Fatalf("у справки нет данных на сервере, а выбор принят: %v", err)
	}
}
