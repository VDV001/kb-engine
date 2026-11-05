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

// Окно присылает только известные модули; чужой id или повтор — отказ, файл
// прежний.
func TestSetModules_Rejects(t *testing.T) {
	tests := []struct {
		name string
		list []desktopconfig.Module
	}{
		{"неизвестный", []desktopconfig.Module{{ID: "chat", Enabled: true}}},
		{"повтор", []desktopconfig.Module{{ID: desktopconfig.ModuleFinances}, {ID: desktopconfig.ModuleFinances, Enabled: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newSourcesSettings(t)
			if err := s.SetModules(tt.list); !errors.Is(err, desktopconfig.ErrModuleID) {
				t.Fatalf("err = %v, want ErrModuleID", err)
			}
			if cfg, _ := desktopconfig.Read(s.path); len(cfg.Modules) != 0 {
				t.Errorf("записано %+v", cfg.Modules)
			}
		})
	}
}
