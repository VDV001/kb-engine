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
// the dictionary) was found: поле, слово этого поля, что совпало (для
// подсветки), и способ.
type Why struct {
	Word  string
	Field Field
	Hit   string
	How   How
}

type field struct {
	name Field
	text string
}

// eachField walks the searched fields in order — единственный список полей:
// по нему склеивает текст поиск (searchable) и разбирает его Explain. Обход, а
// не срез: поиск зовет его на каждую запись каждой буквы, и срез стоил бы
// аллокацию на запись.
func eachField(e domain.Entry, yield func(Field, string)) {
	yield(FieldTitle, e.Title())
	yield(FieldDescription, e.Description())
	yield(FieldCategory, e.Category().String())
	for _, t := range e.Tags() {
		yield(FieldTag, t)
	}
	if a := e.Author(); a != "" {
		yield(FieldAuthor, a)
	}
}

// fieldsOf lists the searched fields for Explain.
func fieldsOf(e domain.Entry) []field {
	var out []field
	eachField(e, func(n Field, t string) { out = append(out, field{n, t}) })
	return out
}

// Explain tells where and how each word of query was found in e — тем же
// кодом, что ищет: слои живут в одном matchHow, а Explain повторяет порядок
// FilterWith и Matches (фраза целиком, затем по словам; запрос, затем его
// синонимы; каждое слово кандидата — по всем полям, лучший слой побеждает).
// Слово, которое не нашлось, в ответе отсутствует.
//
// ponytail: порядок matchesAll (фраза при первом слове без «#», затем по
// словам) здесь повторен, а не взят из одного обхода: FilterWith — горячий
// путь, и объяснения на каждую запись каждой буквы он не несет. Потолок:
// правка matchesAll без правки здесь разведет поиск и объяснение. Держат два
// теста-обязательства (все найденное объяснено, ненайденное — нет). Путь:
// общий обход, возвращающий ([]Why, bool), если бенчмарк FilterWith стерпит.
func Explain(e domain.Entry, query string, m Matcher) []Why {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	fs := joinFields(fieldsOf(e))
	if len(words) > 1 && !strings.HasPrefix(words[0], "#") {
		if why, ok := m.explainQuery(fs, strings.Join(words, " ")); ok {
			return why
		}
	}
	var out []Why
	for _, w := range words {
		if id, ok := strings.CutPrefix(w, "#"); ok {
			if strconv.Itoa(e.ID()) == id {
				out = append(out, Why{Word: w, Field: FieldID, Hit: id, How: HowExact})
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
func (m Matcher) explainQuery(fs joined, query string) ([]Why, bool) {
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
func (m Matcher) explainAll(fs joined, cand string) ([]Why, bool) {
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

// joined is the text the search reads — the fields lowered and joined by a
// space — with where each field starts in it and in its transliteration.
// Translit идет посимвольно, поэтому Translit(a + " " + b) = Translit(a) + " " +
// Translit(b), и у транслита свои начала полей.
type joined struct {
	text, tr        string
	fields          []field
	starts, trStart []int
}

// joinText is the search text as Matches sees it (норма уже применена) and
// where each field starts in it.
func joinText(fs []field) (string, []int) {
	var b strings.Builder
	starts := make([]int, len(fs))
	for i, f := range fs {
		if i > 0 {
			b.WriteByte(' ')
		}
		starts[i] = b.Len()
		b.WriteString(strings.ReplaceAll(strings.ToLower(f.text), "ё", "е"))
	}
	return b.String(), starts
}

// joinFields adds the transliteration for Explain.
func joinFields(fs []field) joined {
	j := joined{fields: fs}
	j.text, j.starts = joinText(fs)
	var t strings.Builder
	for i, f := range fs {
		if i > 0 {
			t.WriteByte(' ')
		}
		j.trStart = append(j.trStart, t.Len())
		t.WriteString(domain.Translit(strings.ReplaceAll(strings.ToLower(f.text), "ё", "е")))
	}
	j.tr = t.String()
	return j
}

// fieldAt returns the field that holds offset off of the text (or of its
// transliteration).
func (j joined) fieldAt(off int, translit bool) field {
	starts := j.starts
	if translit {
		starts = j.trStart
	}
	i := 0
	for k, st := range starts {
		if st <= off {
			i = k
		}
	}
	return j.fields[i]
}

// explainWord asks matchHow over the same text the search reads — слой и стыки
// полей те же, что у поиска, — и называет поле, где совпадение начинается.
func (m Matcher) explainWord(fs joined, w string) (Why, bool) {
	how, form, ok := m.matchHow(fs.text, w)
	if !ok {
		return Why{}, false
	}
	tr := how == HowTranslit || how == HowTypo
	hay := fs.text
	if tr {
		hay = fs.tr
	}
	off := locate(hay, how, form)
	f := fs.fieldAt(off, tr)
	hit, label := hitIn(f.text, how, form)
	return Why{Word: w, Field: f.name, Hit: hit, How: label}, true
}

// locate returns where the match of form starts in hay, the way contains and
// nearAnyWord find it: подстрокой, с начала слова или словом в пределах правок.
func locate(hay string, how How, form string) int {
	if how == HowTypo {
		limit := editLimit(form)
		for _, sp := range wordSpans(hay) {
			if within(sp.w, form, limit) {
				return sp.at
			}
		}
		return 0
	}
	if utf8.RuneCountInString(form) >= shortQuery || strings.ContainsFunc(form, isSeparator) {
		return max(0, strings.Index(hay, form))
	}
	for _, sp := range wordSpans(hay) {
		if strings.HasPrefix(sp.w, form) {
			return sp.at
		}
	}
	return 0
}

type span struct {
	at int
	w  string
}

// wordSpans is wordsOf with where each word starts.
func wordSpans(s string) []span {
	var out []span
	start := -1
	for i, r := range s {
		if isSeparator(r) {
			if start >= 0 {
				out = append(out, span{start, s[start:i]})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, span{start, s[start:]})
	}
	return out
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
