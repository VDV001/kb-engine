// Package search answers whether a catalog entry is what the person is looking
// for, when the words they typed are not the words that were written down.
//
// Замер по живому каталогу: из двенадцати запросов, заданных обычными словами,
// восемь не находили НИЧЕГО, при том что тему база знает — kubernetes десять
// записей, docker восемнадцать, промпт сорок две, anthropic шестьдесят три.
// То есть база помнила прочитанное, а достать его можно было, только вспомнив
// точную формулировку.
//
// Слоёв три, и они разной цены, поэтому разделены. Подстрока — основной путь,
// он не меняется. Транслитерация и расстояние редактирования — чистая
// арифметика без единой зависимости. Словарь синонимов лежит рядом с каталогом
// и правится глазами. ⚠️ Смысловой близости здесь НЕТ: «как оценивать модели»
// не найдёт evals никаким из трёх слоёв, и это сказано вслух, потому что
// «ничего не нашлось» и «этого слоя не существует» — разные ответы.
package search

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
)

// Dictionary — как человек спрашивает → как это записано в базе.
type Dictionary map[string]Terms

// Terms — две РАЗНЫЕ связи одного понятия.
//
// Same — равнозначные написания, связь в обе стороны: спросивший
// «конкурентность» должен найти concurrency, и наоборот.
//
// Includes — что входит в тему, связь только ОТ темы. Разделено замером:
// пока redis лежал равнозначным кешированию, запрос «redis» отдавал 26 записей
// при семи, где это слово есть. Спросив тему, честно получить продукт; спросив
// продукт, получить всю тему — это ответ не на заданный вопрос.
type Terms struct {
	Same     []string
	Includes []string
}

// Matcher сравнивает запрос с текстом записи по трём слоям.
type Matcher struct {
	syn map[string][]string
}

// New builds a matcher over the dictionary. Nil is legal: без словаря работают
// подстрока, транслитерация и опечатки, а слой перевода честно отсутствует.
func New(d Dictionary) Matcher {
	syn := map[string][]string{}
	add := func(from, to string) {
		k := norm(from)
		if k == "" || norm(to) == "" {
			return
		}
		syn[k] = append(syn[k], norm(to))
	}
	for k, t := range d {
		vs := append(append([]string{}, t.Same...), t.Includes...)
		for _, v := range vs {
			add(k, v)
			add(v, k)
			for _, other := range vs {
				if other != v {
					add(v, other) // равнозначные формы связаны и между собой
				}
			}
		}
	}
	return Matcher{syn: syn}
}

// Matches reports whether haystack answers the query.
//
// Запрос целиком проверяется по словарю ДО разбора на слова: «сборка мусора»
// переводится фразой, а по словам не переводится вовсе.
func (m Matcher) Matches(haystack, query string) bool {
	h := norm(haystack)
	q := norm(query)
	if q == "" {
		return true
	}
	for _, cand := range append([]string{q}, m.syn[q]...) {
		if m.allWordsMatch(h, cand) {
			return true
		}
	}
	return false
}

func (m Matcher) allWordsMatch(haystack, query string) bool {
	for w := range strings.FieldsSeq(query) {
		if !m.wordMatches(haystack, w) {
			return false
		}
	}
	return true
}

func (m Matcher) wordMatches(haystack, w string) bool {
	if contains(haystack, w) {
		return true
	}
	for _, s := range m.syn[w] {
		if contains(haystack, s) {
			return true
		}
	}
	tw := domain.Translit(w)
	th := domain.Translit(haystack)
	if tw != w && contains(th, tw) {
		return true
	}
	return nearAnyWord(th, tw)
}

// shortQuery — длина, начиная с которой подстрока внутри чужого слова врёт
// чаще, чем помогает.
//
// Замер по живому каталогу: «sse» отдавал 34 записи, из них 33 — попадание
// внутрь слов вроде «processes», причём верхняя запись выдачи была про
// delivery в командах. Аббревиатуры и есть самые точные запросы, какие бывают
// (SSE, gc, k8s, rag), и основной путь портил именно их.
const shortQuery = 4

// contains — «есть ли запрос в тексте» с поправкой на короткие запросы.
//
// Длинный ищется как прежде, любой подстрокой: «поточн» обязан находить
// «многопоточность». Короткий обязан попадать в НАЧАЛО слова, иначе три буквы
// находятся внутри половины словаря. Начало, а не полное равенство, потому что
// «gc» должен доставать «gcp», а границу слова ставит не только пробел —
// в живых заголовках это дефисы, скобки и двоеточия.
func contains(haystack, query string) bool {
	if utf8.RuneCountInString(query) >= shortQuery || strings.ContainsFunc(query, isSeparator) {
		return strings.Contains(haystack, query)
	}
	for _, hw := range wordsOf(haystack) {
		if strings.HasPrefix(hw, query) {
			return true
		}
	}
	return false
}

// nearAnyWord — есть ли в тексте слово, отличающееся от запрошенного на
// допустимое число правок.
func nearAnyWord(haystack, w string) bool {
	limit := editLimit(w)
	if limit == 0 {
		return false
	}
	for _, hw := range wordsOf(haystack) {
		if within(hw, w, limit) {
			return true
		}
	}
	return false
}

// wordsOf режет текст по границам слов, а не по пробелам.
//
// Разница не косметическая: «промпт-инженерия» по пробелам остаётся одним
// словом, и опечатка «промт» до него не дотягивается никаким разумным порогом.
// Настоящие заголовки полны дефисов, скобок и двоеточий.
func wordsOf(s string) []string {
	return strings.FieldsFunc(s, isSeparator)
}

func isSeparator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// editLimit — сколько правок прощается слову такой длины.
//
// Короткому не прощается ничего: из трёх букв двумя правками получается
// половина словаря, и поиск начнёт находить что попало. Порог не выдуман от
// балды — он проверен отрицательным контролем в наборе приёмки.
func editLimit(w string) int {
	switch n := utf8.RuneCountInString(w); {
	case n <= 4:
		return 0
	case n <= 7:
		return 1
	default:
		return 2
	}
}

// within reports whether a and b differ by at most limit edits.
func within(a, b string, limit int) bool {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > limit {
		return false // длины разошлись сильнее лимита — считать нечего
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			best = min(best, cur[j])
		}
		if best > limit {
			return false // вся строка уже дальше лимита — дальше не станет ближе
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)] <= limit
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// norm приводит текст к виду, в котором слова ещё различимы: регистр и «ё»
// снимаются, но пробелы остаются — в отличие от domain.FoldName, который
// склеивает имя счёта в одно слово.
func norm(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "ё", "е"))
}
