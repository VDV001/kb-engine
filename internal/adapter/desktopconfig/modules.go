package desktopconfig

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// ModuleID — модуль окна: раздел, который человек включает в настройках и
// который открывается вкладкой рядом с поиском (решение владельца 05.11.2026).
type ModuleID string

// Модули окна.
const (
	// ModuleFinances — финансы: витрина движка во вкладке окна.
	ModuleFinances ModuleID = "finances"
	// ModuleChat — чат над документами (#436).
	ModuleChat ModuleID = "chat"
	// ModuleStats — статистика: токены, вызовы модели, активность.
	ModuleStats ModuleID = "stats"
	// ModuleAccess — доступы к продуктам: хранилище с Touch ID (#494).
	ModuleAccess ModuleID = "access"
	// ModuleMemory — память: два слоя, очередь на одобрение, потолок (ADR-0008).
	ModuleMemory ModuleID = "memory"
	// ModuleBrowser — браузер во вкладке: Chrome и т. п. на этом Mac, картинка и ввод через kbengine browser serve.
	ModuleBrowser ModuleID = "browser"
	// ModuleHelp — справка: команды движка и клавиши окна с поиском по задаче.
	ModuleHelp ModuleID = "help"
)

// Ошибки списка модулей от окна; вызывающие проверяют errors.Is.
var (
	ErrModuleID      = errors.New("у модуля нет id или он повторяется")
	ErrUnknownModule = errors.New("модуль неизвестен этой сборке окна")
	ErrModuleServer  = errors.New("сервер модуля не подходит")
)

// Module — включен ли модуль. Порядок в Config.Modules — порядок вкладок.
//
// ponytail: модули зашиты в сборку, магазина нет. Потолок — модуль ставится
// только выпуском окна; путь — манифест модуля и подпись, когда появится
// второй автор модулей.
type Module struct {
	ID      ModuleID `json:"id"`
	Enabled bool     `json:"enabled"`
	// Server — откуда модуль с данными (финансы) их берет: пусто — первый
	// включенный kb-сервер из источников; id сервера из источников; адрес
	// kbengine serve на этом Mac (без токена). Решение владельца 07.11: у кого-то
	// данные на сервере, у кого-то свой serve локально.
	Server string `json:"server,omitempty"`
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

// ModuleServer returns where the module takes its data; пусто — первый
// включенный kb-сервер.
func (c Config) ModuleServer(id ModuleID) string {
	for _, m := range c.Modules {
		if m.ID == id {
			return m.Server
		}
	}
	return ""
}

// IsLocalServer reports whether server is an address (а не id источника).
func IsLocalServer(server string) bool { return strings.Contains(server, "://") }

// CheckModuleServer checks a choice of server for the module: только у
// финансов; адрес — только этого Mac (в сеть токен нужен, а он живет у
// источника); id — сервера из источников (выключенный тоже: включат позже).
func (c Config) CheckModuleServer(id ModuleID, server string) error {
	if id != ModuleFinances {
		return fmt.Errorf("%w: у модуля %q нет данных на сервере", ErrModuleServer, id)
	}
	switch {
	case server == "":
		return nil
	case IsLocalServer(server):
		if err := CheckServerURL(server); err != nil {
			return fmt.Errorf("%w: %w", ErrModuleServer, err)
		}
		if u, _ := url.Parse(strings.TrimSpace(server)); !loopback.Host(u.Hostname()) {
			return fmt.Errorf("%w: адрес — только этого Mac; сервер в сети добавьте в «Источники» с токеном", ErrModuleServer)
		}
		return nil
	}
	for _, s := range c.Sources {
		if s.ID == server {
			if s.Kind != KindServer {
				return fmt.Errorf("%w: источник %q — не kb-сервер", ErrModuleServer, server)
			}
			return nil
		}
	}
	if strings.ContainsAny(server, ".:") { // похоже на адрес без схемы
		return fmt.Errorf("%w: адрес пишется со схемой, например http://%s", ErrModuleServer, server)
	}
	return fmt.Errorf("%w: сервера %q нет в «Источниках»", ErrModuleServer, server)
}
