package search

import (
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Отсюда фильтрация видна ОБЕИМ поверхностям. Раньше она жила в adapter/tui, и
// HTTP-витрина позвать её не могла: фронт фильтровал подстрокой на TypeScript,
// поэтому «кубернетес» давал 10 записей в терминале и ноль в браузере (#252).
// Тот же класс, что «момент записи» до v0.17.0 — одно правило, два писателя.

// Filter narrows entries to those matching every word of the query.
//
// Words are ANDed because a search that widens with each term you add is a
// search you stop trusting after the second word. A word starting with '#' is
// an exact id — the only way to reach entry 3 without also getting 13 and 300.
func Filter(entries []domain.Entry, query string) []domain.Entry {
	return FilterWith(entries, query, New(nil))
}

// FilterWith is Filter with the synonym layer supplied.
//
// Разделено, потому что словарь необязателен: без него поиск обязан работать
// подстрокой, транслитерацией и опечатками, а слой перевода честно отсутствует.
func FilterWith(entries []domain.Entry, query string, m Matcher) []domain.Entry {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return entries
	}

	out := make([]domain.Entry, 0, len(entries))
	for _, e := range entries {
		if matchesAll(e, words, m) {
			out = append(out, e)
		}
	}
	return out
}

func matchesAll(e domain.Entry, words []string, m Matcher) bool {
	haystack := searchable(e)
	// Запрос целиком отдаётся сравнению первым: словарь переводит фразами
	// («сборка мусора»), а по словам такая пара не переводится вовсе.
	if len(words) > 1 && !strings.HasPrefix(words[0], "#") && m.Matches(haystack, strings.Join(words, " ")) {
		return true
	}
	for _, w := range words {
		if id, ok := strings.CutPrefix(w, "#"); ok {
			if strconv.Itoa(e.ID()) != id {
				return false
			}
			continue
		}
		if !m.Matches(haystack, w) {
			return false
		}
	}
	return true
}

// searchable is everything a person would type looking for this entry: its
// title, what it is about, and where it was filed.
func searchable(e domain.Entry) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(e.Title()))
	b.WriteByte(' ')
	b.WriteString(strings.ToLower(e.Category().String()))
	for _, t := range e.Tags() {
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(t))
	}
	if a := e.Author(); a != "" {
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(a))
	}
	return b.String()
}
