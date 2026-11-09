package search

import (
	"slices"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Отсюда фильтрация видна ОБЕИМ поверхностям. Раньше она жила в adapter/tui, и
// HTTP-витрина позвать ее не могла: фронт фильтровал подстрокой на TypeScript,
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
	return rank(out, query, m)
}

// fieldWeight is how much a word found in a field says about the entry:
// название — то, о чем запись, метки и автор — то, чем ее пометили, описание —
// где слово лишь упомянуто.
var fieldWeight = map[Field]int{FieldTitle: 8, FieldTag: 4, FieldAuthor: 4, FieldCategory: 2, FieldDescription: 1, FieldID: 8}

// rank orders found entries by where their words were found (протыкивание
// 06.11: по «SLA» статья с SLA в названии стояла восьмой — выдача шла по
// номеру записи). Целое слово весит вдвое против его начала или перевода:
// «SLA» выше «Slack». Равные остаются в порядке каталога.
//
// Объяснение строится только для найденного: FilterWith на каждую букву по
// всему каталогу его не несет (см. ponytail у Explain).
//
// ponytail: каждое найденное объясняется заново — второй проход по полям.
// Потолок: широкий запрос по синтетическим 1 500 записям — 9,5 мс вместо 5,6
// (опечатка 14 вместо 7, BenchmarkFilter); на живом каталоге 1 619 записей
// «агент» (652 находки) команда целиком та же ~0,28 с. Путь: общий обход
// matchesAll+Explain, возвращающий ([]Why, bool) (тот же, что у Explain).
func rank(found []domain.Entry, query string, m Matcher) []domain.Entry {
	if len(found) < 2 {
		return found
	}
	type scored struct {
		e domain.Entry
		s int
	}
	all := make([]scored, len(found))
	for i, e := range found {
		all[i].e = e
		for _, w := range Explain(e, query, m) {
			v := fieldWeight[w.Field]
			if w.How == HowExact && strings.EqualFold(w.Hit, w.Word) {
				v *= 2
			}
			all[i].s += v
		}
	}
	slices.SortStableFunc(all, func(a, b scored) int { return b.s - a.s })
	for i := range all {
		found[i] = all[i].e
	}
	return found
}

func matchesAll(e domain.Entry, words []string, m Matcher) bool {
	haystack := searchable(e)
	// Запрос целиком отдается сравнению первым: словарь переводит фразами
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
//
// Описание входит сюда, потому что смысл записи живет именно там. Замер по
// живому каталогу: «контекст» — 124 записи с описанием против 29 без него,
// «промпт» — 125 против 42. Витрина искала по описанию с первого дня, терминал
// не искал никогда, и разрыв держался ровно до тех пор, пока обе поверхности
// не стали спрашивать одного ответчика (#252).
//
// Поля перечислены один раз, в fieldsOf (explain.go): Explain разбирает ту же
// склейку, и новое поле здесь не может остаться необъясненным.
func searchable(e domain.Entry) string {
	var b strings.Builder
	first := true
	eachField(e, func(_ Field, t string) {
		if !first {
			b.WriteByte(' ') // и между пустыми полями: склейка Explain та же
		}
		first = false
		b.WriteString(strings.ToLower(t))
	})
	return b.String()
}
