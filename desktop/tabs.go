package main

import (
	"fmt"
	"maps"
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

// Tabs returns the search tabs after «Все»: сохраненные или набор по умолчанию.
func (f *folderSettings) Tabs() []desktopconfig.Tab {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cfg.TabList())
}

// SetTabs saves the search tabs. Индекс не трогается: вкладка — вид выдачи.
// Новая вкладка по источнику обязана указывать на источник из списка: иначе
// она была бы пустой всегда. Уже сохраненная вкладка убранного источника
// проходит — иначе она блокировала бы любую правку остальных (ревью #431);
// окно показывает ее пустой с пометкой. Повтор вида и значения — отказ.
func (f *folderSettings) SetTabs(list []desktopconfig.Tab) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	saved := f.cfg.TabList()
	seen := make(map[string]bool, len(list))
	for _, t := range list {
		// Сохраненная раньше вкладка проходит как есть (полное совпадение):
		// неизвестный вид от новой сборки окно возвращает при каждой правке.
		// Вкладке убранного источника хватает того же вида и значения — ее можно
		// переименовать.
		kept := slices.ContainsFunc(saved, func(x desktopconfig.Tab) bool { return x.Key() == t.Key() })
		if err := t.Check(); err != nil && !slices.Contains(saved, t) {
			return err
		}
		if seen[t.Key()] {
			return fmt.Errorf("%w: вкладка %s повторяется", desktopconfig.ErrTab, t.Key())
		}
		seen[t.Key()] = true
		known := slices.ContainsFunc(f.cfg.Sources, func(s desktopconfig.Source) bool { return s.ID == t.Value })
		if t.Kind == desktopconfig.TabBySource && !known && !kept {
			return fmt.Errorf("%w: источника %q нет в списке", desktopconfig.ErrTab, t.Value)
		}
	}
	next := f.cfg
	next.Tabs = append([]desktopconfig.Tab{}, list...) // не nil: пустой список — выбор «только Все»
	next.TabsV = desktopconfig.TabsVersion             // набор решил человек: убранные «Артефакты» не вернутся
	// Под замком config.json, как SetSources и SetModules.
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false)
		return nil
	})
}

// Accents returns the window's look: режим и свой набор человека.
func (f *folderSettings) Accents() desktopconfig.Accents {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.cfg.Accents
	a.Off = slices.Clone(a.Off)
	a.Look = maps.Clone(a.Look) // как Off: вызывающий не правит конфиг под замком в обход
	return a
}

// SetAccents saves the window's look. Индекс не трогается: оформление —
// только вид окна.
func (f *folderSettings) SetAccents(a desktopconfig.Accents) error {
	if err := a.Check(); err != nil {
		return err
	}
	a.Off = slices.Clone(a.Off)
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Accents = a
	// Под замком config.json, как SetTabs.
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false)
		return nil
	})
}
