package memory

import (
	"math"
	"slices"
	"strings"
	"unicode"
)

// stemRunes — сколько знаков слова остается после грубого усечения.
// ponytail: усечение до пяти знаков вместо настоящего стеммера. Склеивает
// «финансы» и «финансовый», но и «правило» с «правильный». Путь вверх — тот же
// searchsyn/стеммер, что у поиска по каталогу.
const stemRunes = 5

func tokens(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, w := range words {
		if r := []rune(w); len(r) > stemRunes {
			words[i] = string(r[:stemRunes])
		}
	}
	return words
}

// corpus — слова записей и статистика, нужная BM25.
type corpus struct {
	docs [][]string
	df   map[string]int // в скольких записях встретилось слово
	avg  float64        // средняя длина записи в словах
}

// newCorpus tokenizes name, topic, description and body of every note. Описание
// считается дважды: это то, что автор записи счел главным.
func newCorpus(texts []NoteText) corpus {
	c := corpus{docs: make([][]string, len(texts)), df: map[string]int{}}
	var total int
	for i, t := range texts {
		d := tokens(strings.Join([]string{t.Note.Name, t.Note.Topic, t.Note.Description, t.Note.Description, t.Body}, " "))
		c.docs[i] = d
		total += len(d)
		for _, w := range slices.Compact(slices.Sorted(slices.Values(d))) {
			c.df[w]++
		}
	}
	c.avg = float64(total) / float64(len(texts))
	return c
}

// score is the BM25 of one record for the query words.
func (c corpus) score(doc, query []string) float64 {
	const k1, b = 1.2, 0.75
	tf := map[string]int{}
	for _, w := range doc {
		tf[w]++
	}
	n := float64(len(c.docs))
	var sum float64
	for _, w := range query {
		f := float64(tf[w])
		if f == 0 {
			continue
		}
		df := float64(c.df[w])
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		sum += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(doc))/c.avg))
	}
	return sum
}

// textSearch ranks notes by BM25 over name, topic, description and body.
// ponytail: индекс строится на каждый запрос; на сотнях записей это доли
// миллисекунды, на десятках тысяч нужен кэш.
func textSearch(texts []NoteText, query string, limit int) []Hit {
	q := slices.Compact(slices.Sorted(slices.Values(tokens(query))))
	if len(q) == 0 || len(texts) == 0 {
		return nil
	}
	c := newCorpus(texts)
	var hits []Hit
	for i, d := range c.docs {
		if sc := c.score(d, q); sc > 0 {
			hits = append(hits, Hit{Note: texts[i].Note, Score: sc})
		}
	}
	sortHits(hits)
	return hits[:min(len(hits), limit)]
}
