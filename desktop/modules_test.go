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
	if got := s.Modules(); !slices.Equal(got, []desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: false}}) {
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
	if !s.Modules()[0].Enabled {
		t.Error("Modules() не видит включенный модуль")
	}
}

// Модуль от более новой сборки, которого это окно не показывает, переживает
// сохранение; источники сохранение модулей не трогает, и наоборот.
func TestSetModules_KeepsUnknownAndSources(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	s.cfg.Modules = []desktopconfig.Module{{ID: "chat", Enabled: true}}
	sources := slices.Clone(s.cfg.Sources)
	next := []desktopconfig.Source{{ID: "folder-9", Kind: desktopconfig.KindFolder, Path: filepath.Join(home, "notes"), Enabled: true}}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ModuleOn("chat") || !cfg.ModuleOn(desktopconfig.ModuleFinances) {
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
		{"неизвестный", []desktopconfig.Module{{ID: "chat", Enabled: true}}, desktopconfig.ErrUnknownModule},
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
	s.cfg.Modules = []desktopconfig.Module{{ID: "chat", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: false}}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	want := []desktopconfig.Module{{ID: "chat", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: true}}
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
