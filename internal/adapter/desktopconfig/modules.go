package desktopconfig

import (
	"errors"
	"slices"
)

// ModuleID — модуль окна: раздел, который человек включает в настройках и
// который открывается вкладкой рядом с поиском (решение владельца 05.11.2026).
type ModuleID string

// ModuleFinances — финансы: витрина движка во вкладке окна.
const ModuleFinances ModuleID = "finances"

// Ошибки списка модулей от окна; вызывающие проверяют errors.Is.
var (
	ErrModuleID      = errors.New("у модуля нет id или он повторяется")
	ErrUnknownModule = errors.New("модуль неизвестен этой сборке окна")
)

// Module — включен ли модуль. Порядок в Config.Modules — порядок вкладок.
//
// ponytail: модули зашиты в сборку, магазина нет. Потолок — модуль ставится
// только выпуском окна; путь — манифест модуля и подпись, когда появится
// второй автор модулей.
type Module struct {
	ID      ModuleID `json:"id"`
	Enabled bool     `json:"enabled"`
}

// dropBadModules отбрасывает записи без id и повторы (остается первая). Не
// ошибка чтения: модули — только вкладки, а конфиг читают и окно, и
// kbengine-mcp; испорченная запись модуля не должна гасить источники.
// Неизвестный id (его записала более новая сборка) хранится, вкладки у него нет.
func (c *Config) dropBadModules() {
	seen := make(map[ModuleID]bool, len(c.Modules))
	c.Modules = slices.DeleteFunc(c.Modules, func(m Module) bool {
		bad := m.ID == "" || seen[m.ID]
		seen[m.ID] = true
		return bad
	})
}

// ModuleOn reports whether the module is enabled; модуль без записи выключен.
func (c Config) ModuleOn(id ModuleID) bool {
	for _, m := range c.Modules {
		if m.ID == id {
			return m.Enabled
		}
	}
	return false
}
