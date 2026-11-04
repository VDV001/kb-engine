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
// кодом, что ищет: слой за слоем (буквально → синоним → транслит → опечатка)
// по всем полям, и первый сработавший слой дает ответ. Слово, которое не
// нашлось, в ответе отсутствует; фраза, найденная только переводом целиком
// («сборка мусора» → garbage collection), объясняется словами перевода.
func Explain(e domain.Entry, query string, m Matcher) []Why {
	words := strings.Fields(norm(query))
	if len(words) == 0 {
		return nil
	}
	fs := fieldsOf(e)
	var out []Why
	for _, w := range words {
		if id, ok := strings.CutPrefix(w, "#"); ok {
			if strconv.Itoa(e.ID()) == id {
				out = append(out, Why{Word: w, Field: FieldID, Text: id, Hit: id, How: HowExact})
			}
			continue
		}
		if why, ok := m.explainWord(fs, w); ok {
			out = append(out, why)
		}
	}
	if len(out) == len(words) || len(words) < 2 || strings.HasPrefix(words[0], "#") {
		return out
	}
	phrase := strings.Join(words, " ")
	for _, cand := range m.syn[phrase] {
		cw := strings.Fields(cand)
		var why []Why
		for _, w := range cw {
			if x, ok := m.explainWord(fs, w); ok {
				x.Word, x.How = phrase, HowSynonym
				why = append(why, x)
			}
		}
		if len(why) == len(cw) {
			return why
		}
	}
	return out
}

// explainWord runs the layers of wordMatches over the fields.
func (m Matcher) explainWord(fs []field, w string) (Why, bool) {
	for _, f := range fs {
		if hit, ok := exactIn(f.text, w); ok {
			return Why{Word: w, Field: f.name, Text: f.text, Hit: hit, How: HowExact}, true
		}
	}
	for _, s := range m.syn[w] {
		for _, f := range fs {
			if hit, ok := exactIn(f.text, s); ok {
				return Why{Word: w, Field: f.name, Text: f.text, Hit: hit, How: HowSynonym}, true
			}
		}
	}
	tw := domain.Translit(w)
	if tw != w {
		for _, f := range fs {
			for _, ow := range wordsOf(f.text) {
				if contains(domain.Translit(norm(ow)), tw) {
					return Why{Word: w, Field: f.name, Text: f.text, Hit: ow, How: HowTranslit}, true
				}
			}
		}
	}
	if limit := editLimit(tw); limit > 0 {
		for _, f := range fs {
			for _, ow := range wordsOf(f.text) {
				nw := norm(ow)
				th := domain.Translit(nw)
				if within(th, tw, limit) {
					// Совпало без единой правки, но через транслит: латинский
					// запрос против кириллицы («zhogov» — «Жогов»).
					how := HowTypo
					if th == tw && nw != tw {
						how = HowTranslit
					}
					return Why{Word: w, Field: f.name, Text: f.text, Hit: ow, How: how}, true
				}
			}
		}
	}
	return Why{}, false
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
