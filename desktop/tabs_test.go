package main

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Без настройки окно показывает набор по умолчанию; сохраненный набор
// пишется в config.json без переиндексации и виден снимку.
func TestSetTabs_SavesWithoutReindex(t *testing.T) {
	s, applied, home := newSourcesSettings(t)
	if got := s.Tabs(); !slices.Equal(got, desktopconfig.DefaultTabs()) {
		t.Fatalf("Tabs() по умолчанию = %+v", got)
	}
	next := []desktopconfig.Source{{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: filepath.Join(home, "notes"), Enabled: true}}
	if err := s.SetSources(next, nil, nil); err != nil {
		t.Fatal(err)
	}
	calls := applied.calls
	tabs := []desktopconfig.Tab{
		{Name: "Заметки", Kind: desktopconfig.TabBySource, Value: "folder-1"},
		{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"},
	}
	if err := s.SetTabs(tabs); err != nil {
		t.Fatal(err)
	}
	if applied.calls != calls {
		t.Error("вкладки вызвали переиндексацию")
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Tabs, tabs) || !slices.Equal(s.Tabs(), tabs) {
		t.Fatalf("в файле %+v, Tabs() %+v", cfg.Tabs, s.Tabs())
	}
	if !slices.Equal(s.snap.Load().cfg.TabList(), tabs) {
		t.Error("снимок настроек не обновлен")
	}
	// Пустой список — только «Все», и он сохраняется как выбор.
	if err := s.SetTabs([]desktopconfig.Tab{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Tabs(); got == nil || len(got) != 0 {
		t.Fatalf("Tabs() после пустого = %#v", got)
	}
}

// Негодная вкладка и вкладка на источник, которого нет в списке, — отказ,
// файл прежний.
func TestSetTabs_Rejects(t *testing.T) {
	tests := []struct {
		name string
		tab  desktopconfig.Tab
	}{
		{"без имени", desktopconfig.Tab{Kind: desktopconfig.TabByType, Value: "doc"}},
		{"неизвестный тип", desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabByType, Value: "video"}},
		{"нет такого источника", desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabBySource, Value: "folder-404"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newSourcesSettings(t)
			if err := s.SetTabs([]desktopconfig.Tab{tt.tab}); !errors.Is(err, desktopconfig.ErrTab) {
				t.Fatalf("err = %v, want ErrTab", err)
			}
			if cfg, _ := desktopconfig.Read(s.path); cfg.Tabs != nil {
				t.Errorf("записано %+v", cfg.Tabs)
			}
		})
	}
}

// Вкладка на источник, убранный позже, не блокирует правку остальных:
// уже сохраненная вкладка проходит, новая на несуществующий — нет (ревью #431).
func TestSetTabs_StaleSourceTabDoesNotBlock(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	notes := desktopconfig.Source{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: filepath.Join(home, "notes"), Enabled: true}
	work := desktopconfig.Source{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: filepath.Join(home, "work"), Enabled: true}
	if err := s.SetSources([]desktopconfig.Source{notes, work}, nil, nil); err != nil {
		t.Fatal(err)
	}
	stale := desktopconfig.Tab{Name: "Заметки", Kind: desktopconfig.TabBySource, Value: "folder-1"}
	code := desktopconfig.Tab{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"}
	if err := s.SetTabs([]desktopconfig.Tab{stale, code}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources([]desktopconfig.Source{work}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTabs([]desktopconfig.Tab{code, stale}); err != nil {
		t.Fatalf("перестановка с вкладкой убранного источника: %v", err)
	}
	other := desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabBySource, Value: "folder-9"}
	if err := s.SetTabs([]desktopconfig.Tab{code, stale, other}); !errors.Is(err, desktopconfig.ErrTab) {
		t.Fatalf("новая вкладка на несуществующий источник: err = %v", err)
	}
}

// Две одинаковые вкладки — отказ: окно различает вкладки по виду и значению.
func TestSetTabs_RejectsDuplicate(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	code := desktopconfig.Tab{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"}
	again := desktopconfig.Tab{Name: "Код 2", Kind: desktopconfig.TabByType, Value: "code"}
	if err := s.SetTabs([]desktopconfig.Tab{code, again}); !errors.Is(err, desktopconfig.ErrTab) {
		t.Fatalf("err = %v, want ErrTab", err)
	}
}
