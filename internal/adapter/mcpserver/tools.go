// Package mcpserver — адаптер, отдающий каталог агентам по протоколу MCP.
//
// Здесь нет ни одного правила предметной области: поиск живёт в
// internal/usecase/search, чтение каталога — в internal/usecase/query. Причина
// не в чистоте ради чистоты, а в измеренной цене второй копии: витрина год
// искала подстрокой на TypeScript, пока то же правило на Go умело четыре слоя,
// и «кубернетес» в браузере давал ноль при десяти в терминале (#252).
package mcpserver

import (
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Querier — порт чтения каталога. Тот же набор методов, что у HTTP-адаптера,
// объявлен здесь заново намеренно: интерфейс принадлежит потребителю, иначе
// один адаптер начал бы зависеть от другого.
type Querier interface {
	Entries() ([]domain.Entry, error)
	Stats() (query.Stats, error)
}

// SearchCatalog отвечает на запрос агента тем же набором, что видит человек.
//
// Матчер приходит снаружи: его нулевое значение — законное «словаря нет», а не
// поломка, ровно как в HTTP-обработчике.
func SearchCatalog(q Querier, m search.Matcher, query string) ([]domain.Entry, error) {
	entries, err := q.Entries()
	if err != nil {
		return nil, err
	}
	return search.FilterWith(entries, query, m), nil
}

// GetEntry возвращает одну запись по её номеру.
//
// Промах — ошибка, а не пустая запись и не ближайшая: агент, получивший чужую
// карточку вместо отказа, не отличит её от правильной.
func GetEntry(q Querier, id int) (domain.Entry, error) {
	entries, err := q.Entries()
	if err != nil {
		return domain.Entry{}, err
	}
	for _, e := range entries {
		if e.ID() == id {
			return e, nil
		}
	}
	return domain.Entry{}, fmt.Errorf("записи #%d в каталоге нет", id)
}
