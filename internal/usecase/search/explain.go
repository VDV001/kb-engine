package search

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
)

// How is how a query word was found in an entry.
type How string

// Способы — те же слои, что у Matches, в том же порядке.
const (
	HowExact    How = "exact"    // буквально: подстрока, у короткого слова — начало слова
	HowSynonym  How = "synonym"  // словарь синонимов
	HowTranslit How = "translit" // транслитерация: «жо» — zhogov
	HowTypo     How = "typo"     // опечатка в пределах editLimit
)

// Field names a searched field of an entry.
type Field string

// Поля — те, что читает searchable, плюс номер для «#123».
const (
	FieldTitle       Field = "title"
	FieldDescription Field = "description"
	FieldCategory    Field = "category"
	FieldTag         Field = "tag"
	FieldAuthor      Field = "author"
	FieldID          Field = "id"
)

// Why is where and how one query word (or the whole query, for a phrase from
// the dictionary) was found: поле, его текст как записан, слово этого текста,
// что совпало (для подсветки), и способ.
type Why struct {
	Word  string
	Field Field
	Text  string
	Hit   string
	How   How
}

type field struct {
	name Field
	text string
}

// fieldsOf lists the searched fields in the order searchable joins them.
func fieldsOf(e domain.Entry) []field {
	out := []field{{FieldTitle, e.Title()}, {FieldDescription, e.Description()}, {FieldCategory, e.Category().String()}}
	for _, t := range e.Tags() {
		out = append(out, field{FieldTag, t})
	}
	if a := e.Author(); a != "" {
		out = append(out, field{FieldAuthor, a})
	}
	return out
}

// Explain tells where and how each word of query was found in e — тем же
// кодом, что ищет: слои живут в одном matchHow, а Explain повторяет порядок
// FilterWith и Matches (фраза целиком, затем по словам; запрос, затем его
// синонимы; каждое слово кандидата — по всем полям, лучший слой побеждает).
// Слово, которое не нашлось, в ответе отсутствует.
func Explain(e domain.Entry, query string, m Matcher) []Why {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	fs := fieldsOf(e)
	if len(words) > 1 && !strings.HasPrefix(words[0], "#") {
		if why, ok := m.explainQuery(fs, strings.Join(words, " ")); ok {
			return why
		}
	}
	var out []Why
	for _, w := range words {
		if id, ok := strings.CutPrefix(w, "#"); ok {
			if strconv.Itoa(e.ID()) == id {
				out = append(out, Why{Word: w, Field: FieldID, Text: id, Hit: id, How: HowExact})
			}
			continue
		}
		if why, ok := m.explainQuery(fs, w); ok {
			out = append(out, why...)
		}
	}
	return out
}

// explainQuery mirrors Matches: сам запрос, затем его синонимы, и каждый
// кандидат должен найтись весь. Найденное синонимом помечено синонимом.
func (m Matcher) explainQuery(fs []field, query string) ([]Why, bool) {
	q := norm(query)
	for i, cand := range append([]string{q}, m.syn[q]...) {
		why, ok := m.explainAll(fs, cand)
		if !ok {
			continue
		}
		if i > 0 {
			for j := range why {
				why[j].Word, why[j].How = q, HowSynonym
			}
		}
		return why, true
	}
	return nil, false
}

// explainAll mirrors allWordsMatch: every word of cand, each in its own field.
func (m Matcher) explainAll(fs []field, cand string) ([]Why, bool) {
	var out []Why
	for w := range strings.FieldsSeq(cand) {
		why, ok := m.explainWord(fs, w)
		if !ok {
			return nil, false
		}
		out = append(out, why)
	}
	return out, len(out) > 0
}

// rank orders the layers as matchHow tries them.
var rank = map[How]int{HowExact: 0, HowSynonym: 1, HowTranslit: 2, HowTypo: 3}

// explainWord asks matchHow over every field and keeps the best layer: так же
// решает поиск, проверяя слои по всему тексту записи; при равенстве — поле,
// что идет раньше.
func (m Matcher) explainWord(fs []field, w string) (Why, bool) {
	var best Why
	found := false
	for _, f := range fs {
		how, form, ok := m.matchHow(norm(f.text), w)
		if !ok || (found && rank[how] >= rank[best.How]) {
			continue
		}
		hit, label := hitIn(f.text, how, form)
		best, found = Why{Word: w, Field: f.name, Text: f.text, Hit: hit, How: label}, true
	}
	return best, found
}

// hitIn finds the word of text to highlight for a match of form found by how;
// не найдя отдельного слова (форма с дефисом, «ci/cd»), подсвечивает саму форму.
// Опечатка без единой правки через транслит названа транслитом: латинский
// запрос против кириллицы («zhogov» — «Жогов»).
func hitIn(text string, how How, form string) (string, How) {
	switch how {
	case HowExact, HowSynonym:
		if hit, ok := exactIn(text, form); ok {
			return hit, how
		}
	case HowTranslit:
		for _, ow := range wordsOf(text) {
			if contains(domain.Translit(norm(ow)), form) {
				return ow, how
			}
		}
	case HowTypo:
		limit := editLimit(form)
		for _, ow := range wordsOf(text) {
			nw := norm(ow)
			th := domain.Translit(nw)
			if !within(th, form, limit) {
				continue
			}
			if th == form && nw != form {
				return ow, HowTranslit
			}
			return ow, how
		}
	}
	return form, how
}

// exactIn reports the word of text that contains w the way contains does:
// длинное слово — подстрокой, короткое — с начала слова; слово с разделителем
// («ci/cd») ищется во всем тексте и подсвечивается само.
func exactIn(text, w string) (string, bool) {
	if strings.ContainsFunc(w, isSeparator) {
		return w, strings.Contains(norm(text), w)
	}
	long := utf8.RuneCountInString(w) >= shortQuery
	for _, ow := range wordsOf(text) {
		nw := norm(ow)
		if (long && strings.Contains(nw, w)) || (!long && strings.HasPrefix(nw, w)) {
			return ow, true
		}
	}
	return "", false
}

// ValidField reports whether f is a field Explain can name — для тех, кто
// принимает признак находки снаружи (окно от чужого сервера).
func ValidField(f string) bool {
	switch Field(f) {
	case FieldTitle, FieldDescription, FieldCategory, FieldTag, FieldAuthor, FieldID:
		return true
	}
	return false
}

// ValidHow reports whether h is a way Explain can name.
func ValidHow(h string) bool {
	_, ok := rank[How(h)]
	return ok
}
