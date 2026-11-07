package main

import (
	"fmt"
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

// knownModules — модули, которые эта сборка окна умеет показать вкладкой;
// порядок — порядок в настройках.
var knownModules = []desktopconfig.ModuleID{desktopconfig.ModuleChat, desktopconfig.ModuleFinances, desktopconfig.ModuleStats, desktopconfig.ModuleAccess}

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
// присланные меняются на своих местах, новые встают в конец, остальные
// (непереданные и модули более новой сборки) остаются как были.
func (f *folderSettings) SetModules(list []desktopconfig.Module) error {
	seen := make(map[desktopconfig.ModuleID]bool, len(list))
	for _, m := range list {
		if !slices.Contains(knownModules, m.ID) {
			return fmt.Errorf("%w: %q", desktopconfig.ErrUnknownModule, m.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("%w: %q", desktopconfig.ErrModuleID, m.ID)
		}
		seen[m.ID] = true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Modules = slices.Clone(f.cfg.Modules)
	for _, m := range list {
		if i := slices.IndexFunc(next.Modules, func(x desktopconfig.Module) bool { return x.ID == m.ID }); i >= 0 {
			next.Modules[i] = m
		} else {
			next.Modules = append(next.Modules, m)
		}
	}
	// Под замком config.json, как SetSources: MCP-сервер читает файл под ним.
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false) // снимок читают без f.mu
		return nil
	})
}
