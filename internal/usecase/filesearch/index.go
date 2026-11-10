// Package filesearch indexes files on disk and finds them by words, the way a
// person remembers a file: by what was written in it or in its name.
package filesearch

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/daniil/kb-engine/internal/domain"
)

// minTokenRunes — короче двух рун в индекс не идет: однобуквенные союзы и
// переменные заполнили бы его, ничего не давая поиску.
const minTokenRunes = 2

// Tokens splits text into lowercase words. «ё» folds to «е»: в текстах
// владельца е не пишут, а в чужих пишут, и одно слово не должно жить под
// двумя написаниями.
func Tokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len([]rune(f)) < minTokenRunes {
			continue
		}
		out = append(out, strings.ReplaceAll(f, "ё", "е"))
	}
	return out
}

// russianEndings — окончания, которые срезаются, длинные раньше коротких.
//
// ponytail: срезание окончаний, а не морфология. Потолок: беглые гласные
// («заявок» не сходится с «заявка»), глаголы и чередования не разбираются.
// Путь апгрейда: Snowball-стеммер или словарь OpenCorpora, когда промахи
// станут заметны на живых запросах.
var russianEndings = []string{
	"иями", "ями", "ами", "ого", "его", "ому", "ему", "ыми", "ими",
	"ией", "ием", "иях", "ях", "ах", "ов", "ев", "ей", "ой", "ий", "ый", "ая", "яя",
	"ое", "ее", "ые", "ие", "ую", "юю", "ом", "ем", "ам", "ям", "ия", "ию", "ии", "ых", "их",
	"а", "я", "о", "е", "ы", "и", "у", "ю", "ь",
}

// minStemRunes — основа короче трех рун перестает различать слова: «кот» и
// «код» сошлись бы в «ко».
const minStemRunes = 3

// Stem reduces a Russian word to its stem by cutting a known ending. Words in
// other scripts and words too short to keep a stem pass through unchanged.
func Stem(word string) string {
	if !isCyrillic(word) {
		return word
	}
	n := len([]rune(word))
	for _, end := range russianEndings {
		if strings.HasSuffix(word, end) && n-len([]rune(end)) >= minStemRunes {
			return strings.TrimSuffix(word, end)
		}
	}
	return word
}

func isCyrillic(word string) bool {
	for _, r := range word {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// terms returns the stems of a text, the unit the index stores and the query
// is matched on.
func terms(text string) []string {
	toks := Tokens(text)
	for i, t := range toks {
		toks[i] = Stem(t)
	}
	return toks
}

// contentTerms — основы содержимого файла без идентификаторов. Отсев только
// здесь: имя файла и запрос человек пишет сам, и «invoice20261103» в имени
// обязан находиться (ревью #374).
func contentTerms(text string) []string {
	toks := Tokens(text)
	out := toks[:0]
	for _, t := range toks {
		if !isIdentifier(t) {
			out = append(out, Stem(t))
		}
	}
	return out
}

// isIdentifier — хеш, UUID, base64, длинный id: «слово», которое уникально и в
// поиске не участвует, а словарь индекса раздувает. Буквы вперемешку с
// цифрами от 8 знаков или любой токен длиннее 32 знаков. Короткие смеси (v2,
// 6ac2) и чистые числа (2026, 152) остаются.
//
// ponytail: эвристика по форме. Потолок: артикул «abc12345» тоже отсеется.
// Путь: оставлять смеси, которые встретились в двух и более файлах.
// Пороги отсева входят в отпечаток правил снимка: поменяешь их — снимок
// соберется заново.
const (
	idMixedRunes = 8
	idMaxRunes   = 32
)

func isIdentifier(t string) bool {
	n := len([]rune(t))
	if n > idMaxRunes {
		return true
	}
	if n < idMixedRunes {
		return false
	}
	var letters, digits bool
	for _, r := range t {
		switch {
		case unicode.IsDigit(r):
			digits = true
		case unicode.IsLetter(r):
			letters = true
		}
	}
	return letters && digits
}

// Hit is one found file with its score and the line that matched.
type Hit struct {
	Doc     domain.FileDoc
	Score   float64
	Snippet string
}

// Index is an inverted index over files held in memory.
type Index struct {
	docs     []domain.FileDoc
	postings map[string][]Posting // основа -> файлы, где она встретилась
	builtAt  time.Time            // когда собран: файлы моложе этого перечитываются
}

// Posting — файл и сколько раз в нем встретилась основа. 8 байт вместо записи
// вложенной карты (десятки байт): на 40 тыс. файлов 1,52 ГБ стало 0,99 ГБ.
type Posting struct {
	Doc int32
	TF  uint32
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{postings: map[string][]Posting{}}
}

// Add indexes a file's text together with its name: имя файла тоже текст, и
// по нему ищут не реже, чем по содержимому.
func (ix *Index) Add(doc domain.FileDoc, text string) {
	id := ix.addDoc(doc)
	counts := map[string]uint32{}
	for _, t := range append(terms(doc.Name()), contentTerms(text)...) {
		counts[t]++
	}
	for t, tf := range counts {
		ix.postings[t] = append(ix.postings[t], Posting{Doc: id, TF: tf})
	}
}

// Len returns how many files the index holds.
func (ix *Index) Len() int { return len(ix.docs) }

// Terms returns how many distinct stems the index holds.
func (ix *Index) Terms() int { return len(ix.postings) }

// Search returns files containing every word of the query, best first.
//
// Нужны ВСЕ слова запроса: человек уточняет запрос, добавляя слово, и ждет,
// что выдача сузится, а не расширится. Вес — насыщаемая частота на редкость
// слова (упрощенный BM25): десятое упоминание значит меньше первого, а слово,
// которое есть почти везде, почти ничего не решает.
func (ix *Index) Search(query string, limit int) []Hit {
	qs := terms(query)
	if len(qs) == 0 || limit <= 0 {
		return nil
	}
	n := float64(len(ix.docs))
	scores := map[int]float64{}
	for i, q := range slices.Compact(slices.Sorted(slices.Values(qs))) {
		p := ix.postings[q]
		if len(p) == 0 {
			return nil
		}
		idf := math.Log(1 + n/float64(len(p)))
		next := make(map[int]float64, len(p))
		for _, ps := range p {
			id := int(ps.Doc)
			if _, ok := scores[id]; i > 0 && !ok {
				continue
			}
			f := float64(ps.TF)
			next[id] = scores[id] + idf*f/(f+1.2)
		}
		scores = next
	}
	hits := make([]Hit, 0, len(scores))
	for id, s := range scores {
		hits = append(hits, Hit{Doc: ix.docs[id], Score: s})
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.Doc.Path(), b.Doc.Path())
	})
	return hits[:min(limit, len(hits))]
}

// Snippet returns the first line of text that contains a word of the query,
// trimmed and cut to width runes around the match. Без совпадения — первая непустая строка: она
// обычно заголовок и говорит о файле больше, чем пустота.
func Snippet(text, query string, width int) string {
	want := map[string]bool{}
	for _, q := range terms(query) {
		want[q] = true
	}
	first := ""
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if at := matchAt(line, want); at >= 0 {
			return around(line, at, width)
		}
	}
	return cut(first, width)
}

// matchAt returns the rune offset of the first word of line whose stem is
// wanted, or -1.
func matchAt(line string, want map[string]bool) int {
	r := []rune(line)
	for i := 0; i < len(r); {
		if !unicode.IsLetter(r[i]) && !unicode.IsDigit(r[i]) {
			i++
			continue
		}
		j := i
		for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j])) {
			j++
		}
		for _, t := range terms(string(r[i:j])) {
			if want[t] {
				return i
			}
		}
		i = j
	}
	return -1
}

// around cuts line to width runes so that the rune at offset at stays visible:
// совпадение в середине длинной строки иначе обрезалось бы вместе с хвостом.
func around(line string, at, width int) string {
	r := []rune(line)
	if len(r) <= width {
		return line
	}
	start := max(0, at-width/3)
	end := min(len(r), start+width)
	start = max(0, end-width)
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

func cut(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width]) + "…"
}

// addDoc registers a file without its words and returns its number.
func (ix *Index) addDoc(doc domain.FileDoc) int32 {
	ix.docs = append(ix.docs, doc)
	return int32(len(ix.docs) - 1)
}

// byPath maps each file's path to its number. Nil-safe: no index, no files.
func (ix *Index) byPath() map[string]int32 {
	m := map[string]int32{}
	if ix == nil {
		return m
	}
	for i, d := range ix.docs {
		m[d.Path()] = int32(i)
	}
	return m
}

// carryOver copies the words of unchanged files from prev, renumbered by
// oldToNew — так файл попадает в новый индекс, не будучи прочитанным.
func (ix *Index) carryOver(prev *Index, oldToNew map[int32]int32) {
	if prev == nil {
		return
	}
	for t, list := range prev.postings {
		for _, p := range list {
			if id, ok := oldToNew[p.Doc]; ok {
				ix.postings[t] = append(ix.postings[t], Posting{Doc: id, TF: p.TF})
			}
		}
	}
}
