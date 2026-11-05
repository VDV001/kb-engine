package main

import (
	"fmt"
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

// knownModules — модули, которые эта сборка окна умеет показать вкладкой;
// порядок — порядок в настройках.
var knownModules = []desktopconfig.ModuleID{desktopconfig.ModuleFinances}

// Modules returns every module this window knows with whether it is on.
func (f *folderSettings) Modules() []desktopconfig.Module {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]desktopconfig.Module, 0, len(knownModules))
	for _, id := range knownModules {
		out = append(out, desktopconfig.Module{ID: id, Enabled: f.cfg.ModuleOn(id)})
	}
	return out
}

// SetModules saves which modules are on. Индекс не трогается: модуль —
// вкладка, а не источник. Окно присылает только известные ему модули;
// модули более новой сборки из конфига сохраняются как были.
func (f *folderSettings) SetModules(list []desktopconfig.Module) error {
	for _, m := range list {
		if !slices.Contains(knownModules, m.ID) {
			return fmt.Errorf("%w: %q неизвестен окну", desktopconfig.ErrModuleID, m.ID)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Modules = slices.Clone(list)
	for _, m := range f.cfg.Modules {
		if !slices.Contains(knownModules, m.ID) {
			next.Modules = append(next.Modules, m)
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	// Под замком config.json, как SetSources: MCP-сервер читает файл под ним.
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		return nil
	})
}
