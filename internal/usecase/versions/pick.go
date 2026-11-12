package versions

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrNoSource — папки окна не подключены: версий нет, и это не сбой чтения.
var ErrNoSource = errors.New("папки окна не подключены")

// Choice — версия документа, как ее описывает человек: дата, подпись «что
// нового», его заметка и путь. Выбор версии словами не знает ни окна, ни чата.
type Choice struct {
	Path, Date, Note, Mark string
	Current                bool // значок «актуальная»
}

var (
	askedNo   = regexp.MustCompile(`(?:\bv|\bв|версия|версию|версии|№|#)\s*(\d+)`)
	askedDate = regexp.MustCompile(`(\d{1,2})[./-](\d{1,2})(?:[./-](\d{2,4}))?`)
)

// Pick chooses the versions (индексы, лента от старой к новой) that the words
// of a person point at: порядок («последняя», «предыдущая», «первая»), номер
// («v3», «версия 2»), дата («от 05.11», день и месяц, год по желанию), значок
// («актуальная») и слова из описания или заметки. Подходящих может быть
// несколько или ни одной: решает человек, а не догадка.
func Pick(vs []Choice, words string) []int {
	w := strings.ToLower(words)
	if len(vs) == 0 {
		return nil
	}
	if out, ok := byOrder(vs, w); ok {
		return out
	}
	if m := askedNo.FindStringSubmatch(w); m != nil {
		if k, err := strconv.Atoi(m[1]); err == nil && k >= 1 && k <= len(vs) {
			return []int{k - 1}
		}
		return nil
	}
	if m := askedDate.FindStringSubmatch(w); m != nil {
		return indexes(vs, func(v Choice) bool { return dateIs(v.Date, m[1], m[2], m[3]) })
	}
	return byWords(vs, w)
}

// byOrder resolves the words about the order or the badge; ok=false — таких слов нет.
func byOrder(vs []Choice, w string) (out []int, ok bool) {
	n := len(vs)
	has := func(stems ...string) bool {
		return slices.ContainsFunc(stems, func(s string) bool { return strings.Contains(w, s) })
	}
	switch {
	case has("предыдущ", "предпоследн"):
		if n < 2 {
			return nil, true
		}
		return []int{n - 2}, true
	case has("последн", "свеж", "нов"):
		return []int{n - 1}, true
	case has("перв", "самая стар"):
		return []int{0}, true
	case has("актуальн", "текущ"):
		return indexes(vs, func(v Choice) bool { return v.Current }), true
	}
	return nil, false
}

func indexes(vs []Choice, ok func(Choice) bool) []int {
	var out []int
	for i, v := range vs {
		if ok(v) {
			out = append(out, i)
		}
	}
	return out
}

// dateIs compares «02.01.2006» with the day, month and (если названа) year of the words.
func dateIs(date, d, m, y string) bool {
	parts := strings.Split(date, ".")
	if len(parts) != 3 {
		return false
	}
	pd, _ := strconv.Atoi(parts[0])
	pm, _ := strconv.Atoi(parts[1])
	wd, _ := strconv.Atoi(d)
	wm, _ := strconv.Atoi(m)
	if pd != wd || pm != wm {
		return false
	}
	if y == "" {
		return true
	}
	wy, _ := strconv.Atoi(y)
	py, _ := strconv.Atoi(parts[2])
	return wy == py || wy+2000 == py
}

// byWords picks the versions whose note, mark or path share the most stems of
// the words: слово считается по первым четырем буквам, чтобы «убрали» нашло
// «убрано», а «отправили» — «отправлено».
func byWords(vs []Choice, w string) []int {
	stems := wordStems(w)
	best, out := 0, []int(nil)
	for i, v := range vs {
		hay := strings.ToLower(v.Note + " " + v.Mark + " " + v.Path)
		score := 0
		for _, s := range stems {
			if strings.Contains(hay, s) {
				score++
			}
		}
		switch {
		case score == 0:
		case score > best:
			best, out = score, []int{i}
		case score == best:
			out = append(out, i)
		}
	}
	return out
}

// wordStems — первые четыре буквы каждого слова от четырех букв.
func wordStems(w string) []string {
	isWord := func(r rune) bool {
		return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' || r == 'ё'
	}
	var stems []string
	for word := range strings.FieldsFuncSeq(w, func(r rune) bool { return !isWord(r) }) {
		if r := []rune(word); len(r) >= 4 {
			stems = append(stems, string(r[:4]))
		}
	}
	return stems
}
