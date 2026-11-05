package main

import (
	"fmt"
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
// Вкладка по источнику обязана указывать на источник из списка: иначе она
// была бы пустой всегда. Источник, убранный позже, вкладку не роняет —
// окно показывает ее пустой.
func (f *folderSettings) SetTabs(list []desktopconfig.Tab) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range list {
		if err := t.Check(); err != nil {
			return err
		}
		if t.Kind == desktopconfig.TabBySource && !slices.ContainsFunc(f.cfg.Sources, func(s desktopconfig.Source) bool { return s.ID == t.Value }) {
			return fmt.Errorf("%w: источника %q нет в списке", desktopconfig.ErrTab, t.Value)
		}
	}
	next := f.cfg
	next.Tabs = append([]desktopconfig.Tab{}, list...) // не nil: пустой список — выбор «только Все»
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
