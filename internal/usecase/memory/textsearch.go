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

// textSearch ranks notes by BM25 over name, topic, description and body.
// Описание считается дважды: это то, что автор записи счел главным.
// ponytail: индекс строится на каждый запрос; на сотнях записей это доли
// миллисекунды, на десятках тысяч нужен кэш.
func textSearch(texts []NoteText, query string, limit int) []Hit {
	q := tokens(query)
	slices.Sort(q)
	q = slices.Compact(q)
	if len(q) == 0 || len(texts) == 0 {
		return nil
	}
	docs := make([][]string, len(texts))
	df := map[string]int{}
	var total float64
	for i, t := range texts {
		d := tokens(strings.Join([]string{t.Note.Name, t.Note.Topic, t.Note.Description, t.Note.Description, t.Body}, " "))
		docs[i] = d
		total += float64(len(d))
		seen := map[string]bool{}
		for _, w := range d {
			if !seen[w] {
				seen[w] = true
				df[w]++
			}
		}
	}
	avg := total / float64(len(texts))
	const k1, b = 1.2, 0.75
	n := float64(len(texts))
	var hits []Hit
	for i, d := range docs {
		tf := map[string]int{}
		for _, w := range d {
			tf[w]++
		}
		var score float64
		for _, w := range q {
			f := float64(tf[w])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[w])+0.5)/(float64(df[w])+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(d))/avg))
		}
		if score > 0 {
			hits = append(hits, Hit{Note: texts[i].Note, Score: score})
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return strings.Compare(a.Note.Name, b.Note.Name)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
