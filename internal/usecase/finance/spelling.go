package finance

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// SpellingForm — одно написание и сколько записей журнала его несут.
type SpellingForm struct {
	Value string
	Count int
	// FromVocabulary — написание пришло из словаря быстрого ввода, а не из
	// журнала. У него Count может быть нулевым, и это самый тревожный случай:
	// словарь ПИШЕТ, поэтому первая же новая покупка вернёт форму, которой в
	// данных нет.
	FromVocabulary bool
}

// SpellingFinding — одно место или счёт, записанные больше чем одним способом.
type SpellingFinding struct {
	Kind   string // "место" | "счёт"
	Forms  []SpellingForm
	Reason string
}

// SpellingIssues finds names written more than one way.
//
// Движок умеет отвечать «одно ли это имя» (domain.FoldName), но найти
// расхождения в уже накопленных данных было нечем: за два дня один класс
// укусил трижды — «КБ» против «К&Б», «Бургер Кинг» против латинского
// написания, канон словаря, которого в журнале не было ни разу.
//
// Решение не принимается за человека: проверка называет ОБЕ формы и число
// записей у каждой. Выбор написания — это выбор, а не арифметика: у «К&Б»
// было 17 записей против 402 у соседней формы, и верной оказалась редкая.
func SpellingIssues(records []Record, voc Vocabulary) []SpellingFinding {
	places, accounts := map[string]int{}, map[string]int{}
	for _, r := range records {
		tx := r.Transaction()
		if p := strings.TrimSpace(tx.Place()); p != "" {
			places[p]++
		}
		if a := strings.TrimSpace(tx.Account()); a != "" {
			accounts[a]++
		}
	}

	findings := groupByName("место", places)
	findings = append(findings, groupByName("счёт", accounts)...)
	findings = append(findings, vocabularyDrift(places, voc)...)
	return findings
}

// groupByName собирает написания, которые движок считает одним именем.
//
// Ключ — транслитерация поверх FoldName, поэтому в одну группу попадают и
// «Пятёрочка» с «Пятерочка» (регистр, «ё», дефисы), и «Бургер Кинг» с «Burger
// King» (разный алфавит).
func groupByName(kind string, counts map[string]int) []SpellingFinding {
	groups := map[string][]string{}
	for name := range counts {
		key := translitKey(name)
		groups[key] = append(groups[key], name)
	}

	var out []SpellingFinding
	for _, key := range sortedKeys(groups) {
		names := groups[key]
		if len(names) < 2 {
			continue
		}
		// Преобладающая форма первой: человек решает, глядя на числа, и
		// начинать разговор удобнее с того написания, которое уже победило.
		sort.Slice(names, func(i, j int) bool {
			if counts[names[i]] != counts[names[j]] {
				return counts[names[i]] > counts[names[j]]
			}
			return names[i] < names[j]
		})
		forms := make([]SpellingForm, 0, len(names))
		for _, n := range names {
			forms = append(forms, SpellingForm{Value: n, Count: counts[n]})
		}
		out = append(out, SpellingFinding{Kind: kind, Forms: forms, Reason: reasonFor(names)})
	}
	return out
}

// vocabularyDrift ловит канон словаря, которого в журнале нет.
//
// Словарь — писатель, история — читатель, и правка одних лишь исторических
// записей отменяется первой же покупкой: ровно так «Италиан Пицца» вернулась
// бы к латинскому написанию, хотя все 12 записей были кириллицей.
func vocabularyDrift(places map[string]int, voc Vocabulary) []SpellingFinding {
	var out []SpellingFinding
	for _, word := range sortedKeys(voc.Places) {
		canon := strings.TrimSpace(voc.Places[word].Place)
		if canon == "" || places[canon] > 0 {
			continue
		}
		// Что же тогда лежит в журнале: место, к которому это слово ведёт.
		// Ищется по самому слову словаря — транслитерация здесь бессильна,
		// «Пицца» и «Pizza» расходятся уже на второй букве.
		var actual []string
		for name := range places {
			if strings.Contains(domain.FoldName(name), domain.FoldName(word)) {
				actual = append(actual, name)
			}
		}
		if len(actual) == 0 {
			continue
		}
		sort.Slice(actual, func(i, j int) bool { return places[actual[i]] > places[actual[j]] })

		forms := []SpellingForm{{Value: canon, Count: 0, FromVocabulary: true}}
		for _, name := range actual {
			forms = append(forms, SpellingForm{Value: name, Count: places[name]})
		}
		out = append(out, SpellingFinding{
			Kind:   "место",
			Forms:  forms,
			Reason: fmt.Sprintf("словарь: ключ %q ведёт на написание, которого в журнале нет ни разу", word),
		})
	}
	return out
}

// reasonFor решает, чем именно различаются написания одной группы.
func reasonFor(names []string) string {
	if len(names) > 1 && hasCyrillic(names[0]) != hasCyrillic(names[1]) {
		return "разный алфавит: одно написание латиницей, другое кириллицей"
	}
	return "разное написание: регистр, «ё» или пробелы"
}

// translitKey приводит имя к сравнимому виду поверх FoldName, переводя
// кириллицу в латиницу.
//
// ⚠️ ponytail: таблица побуквенная, поэтому связывает «Бургер Кинг» с «Burger
// King», но НЕ свяжет «Италиан Пицца» с «Italian Pizza» — «ц» становится «ts»,
// а не «z». Потолок назван честно: полное сопоставление требует словаря
// заимствований, а не таблицы букв. Такие пары ловит проверка словаря, где
// связь идёт по ключу, а не по звучанию.
func translitKey(s string) string {
	return domain.Translit(domain.FoldName(s))
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 'а' && r <= 'я' || r >= 'А' && r <= 'Я' || r == 'ё' || r == 'Ё' {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
