package desktopconfig

import (
	"errors"
	"fmt"
)

// ModuleID — модуль окна: раздел, который человек включает в настройках и
// который открывается вкладкой рядом с поиском (решение владельца 05.11.2026).
type ModuleID string

// ModuleFinances — финансы: витрина движка во вкладке окна.
const ModuleFinances ModuleID = "finances"

// ErrModuleID — у модуля нет id или он повторяется.
var ErrModuleID = errors.New("у модуля нет id или он повторяется")

// Module — включен ли модуль. Порядок в Config.Modules — порядок вкладок.
//
// ponytail: модули зашиты в сборку, магазина нет. Потолок — модуль ставится
// только выпуском окна; путь — манифест модуля и подпись, когда появится
// второй автор модулей.
type Module struct {
	ID      ModuleID `json:"id"`
	Enabled bool     `json:"enabled"`
}

// validateModules checks ids are present and unique. Неизвестный id — не
// ошибка: его записала более новая сборка, он хранится, вкладки у него нет.
func (c Config) validateModules() error {
	seen := make(map[ModuleID]bool, len(c.Modules))
	for _, m := range c.Modules {
		if m.ID == "" || seen[m.ID] {
			return fmt.Errorf("%w: %q", ErrModuleID, m.ID)
		}
		seen[m.ID] = true
	}
	return nil
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
