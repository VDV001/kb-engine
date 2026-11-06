package desktopconfig

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Подключения (решение владельца 06.11, вариант А): куда и с каким доступом
// ходит окно. Здесь — без секретов: вид, подпись, чей вход, включено ли;
// токены лежат в Связке ключей по id подключения. Источники и чат ссылаются
// на подключение по id.

// ConnKind is what an access is to.
type ConnKind string

// ConnGoogle — Google Диск: вход OAuth, только чтение.
const ConnGoogle ConnKind = "google"

// ErrConnections — список подключений не подходит.
var ErrConnections = errors.New("подключения не подходят")

// Connection is one access the window holds.
type Connection struct {
	ID      string   `json:"id"`
	Kind    ConnKind `json:"kind"`
	Name    string   `json:"name,omitempty"`
	Account string   `json:"account,omitempty"` // чей вход: e-mail
	Enabled bool     `json:"enabled"`
}

// CheckConnections validates the list: id есть и без повторов, подпись не
// длиннее 80 знаков. Неизвестный вид — не ошибка: его записала новая сборка.
func CheckConnections(list []Connection) error {
	seen := map[string]bool{}
	for _, c := range list {
		switch {
		case c.ID == "":
			return fmt.Errorf("%w: подключение без id", ErrConnections)
		case seen[c.ID]:
			return fmt.Errorf("%w: id %q дважды", ErrConnections, c.ID)
		case utf8.RuneCountInString(c.Name) > 80:
			return fmt.Errorf("%w: подпись длиннее 80 знаков", ErrConnections)
		}
		seen[c.ID] = true
	}
	return nil
}

// Connection returns the connection id.
func (c Config) Connection(id string) (Connection, bool) {
	for _, x := range c.Connections {
		if x.ID == id {
			return x, true
		}
	}
	return Connection{}, false
}
