// Package tui renders the catalog in a terminal. It is a second face on the
// same use cases the web dashboard uses — not a reduced copy of them.
package tui

import (
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Filter — вход терминала в фильтрацию каталога.
//
// Само правило живёт в usecase/search: оно отвечает на вопрос «что считается
// совпадением по записи», а это предметная область, а не устройство терминала.
// Пока правило лежало здесь, второй адаптер позвать его не мог и завёл свою
// копию на TypeScript — #252.
func Filter(entries []domain.Entry, query string) []domain.Entry {
	return search.Filter(entries, query)
}

// FilterWith is Filter with the synonym layer supplied.
func FilterWith(entries []domain.Entry, query string, m search.Matcher) []domain.Entry {
	return search.FilterWith(entries, query, m)
}
