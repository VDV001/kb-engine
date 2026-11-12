package chat

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Versions — итерации документов для режима «модель ищет» (#547): те же
// семейства, подписи и отметки, что на вкладке «Артефакты».
type Versions interface {
	// Families — семейства по запросу, версии в каждом от старой к новой.
	Families(ctx context.Context, q string) ([]VersionFamily, error)
	// Text — текст версии по пути из Families.
	Text(ctx context.Context, path string) (string, error)
}

// VersionFamily — документ и его версии.
type VersionFamily struct {
	Name     string
	Versions []VersionInfo
}

// VersionInfo — одна версия: что нового и что отметил человек.
type VersionInfo struct {
	Path    string
	Date    string // 02.01.2006
	Size    int64
	Note    string // что нового относительно предыдущей
	Mark    string // заметка человека
	Current bool   // значок «актуальная»
	Dups    []string
}

// maxFamilies — сколько документов версий модель получает в одном ответе.
const maxFamilies = 5

// versionsPrompt is added when the window has the versions of documents.
const versionsPrompt = `
У документов бывают версии (итерации). Инструмент versions показывает версии с описанием «что нового» и пометками человека, open_version берет текст нужной версии: «последняя», «предыдущая», «первая», «v3», «от 05.11», «актуальная» или слова из заметки («отправленная Алексею»). Если подходит несколько версий или документов, спроси человека, а не угадывай.`

var versionTools = []Tool{
	{Name: "versions", Description: "Версии документа: даты, что нового в каждой, пометки человека и значок «актуальная». Ищет по имени документа.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"имя документа или его часть"}},"required":["q"]}`)},
	{Name: "open_version", Description: "Прочитать текст одной версии документа. which — словами человека: последняя, предыдущая, первая, v3, от 05.11, актуальная или слова из его заметки.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"имя документа"},"which":{"type":"string","description":"какая версия"}},"required":["q","which"]}`)},
}

// listVersions answers the versions tool.
func (r *toolRun) listVersions(ctx context.Context, q string) string {
	fams, err := r.versions.Families(ctx, q)
	if err != nil {
		return "ошибка версий: " + err.Error()
	}
	if len(fams) == 0 {
		return "ничего не найдено"
	}
	var b strings.Builder
	for i, f := range fams {
		if i == maxFamilies {
			fmt.Fprintf(&b, "…и еще документов: %d, уточните имя\n", len(fams)-maxFamilies)
			break
		}
		fmt.Fprintf(&b, "%s\n", f.Name)
		for n, v := range f.Versions {
			fmt.Fprintf(&b, "  %d. %s\n", n+1, versionLine(v))
		}
	}
	return b.String()
}

func versionLine(v VersionInfo) string {
	line := v.Date + " · " + v.Path
	if v.Note != "" {
		line += " · " + trimRunes(v.Note, searchSnip)
	}
	if v.Mark != "" {
		line += " · заметка: " + trimRunes(v.Mark, searchSnip)
	}
	if v.Current {
		line += " · актуальная"
	}
	return line
}

// openVersion answers open_version: одна версия читается, при сомнении модель
// получает список и вопрос к человеку, а не догадку.
func (r *toolRun) openVersion(ctx context.Context, q, which string) string {
	fams, err := r.versions.Families(ctx, q)
	if err != nil {
		return "ошибка версий: " + err.Error()
	}
	switch {
	case len(fams) == 0:
		return "ничего не найдено"
	case len(fams) > 1:
		var names []string
		for _, f := range fams {
			names = append(names, f.Name)
		}
		return "найдено несколько документов, спроси человека, какой нужен: " + strings.Join(names, "; ")
	case strings.TrimSpace(which) == "":
		return "не сказано, какую именно версию; спроси человека. Есть:\n" + r.listVersions(ctx, fams[0].Name)
	}
	f := fams[0]
	picked := pickVersions(f.Versions, which)
	switch {
	case len(picked) == 0:
		return "такой версии нет, спроси человека. Есть:\n" + r.listVersions(ctx, f.Name)
	case len(picked) > 1:
		var b strings.Builder
		b.WriteString("подходят несколько версий, спроси человека, какая нужна:\n")
		for _, i := range picked {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, versionLine(f.Versions[i]))
		}
		return b.String()
	}
	v := f.Versions[picked[0]]
	text, err := r.versions.Text(ctx, v.Path)
	if err != nil {
		return "ошибка чтения версии " + v.Path + ": " + err.Error()
	}
	if strings.TrimSpace(text) == "" {
		return "у версии " + v.Path + " нет текста (скан без распознавания или пустой файл)"
	}
	title := f.Name + " · " + v.Date
	r.read = appendNew(r.read, Doc{Ref: v.Path, Title: title, Source: "версии", Text: text})
	return title + "\n" + trimRunes(text, MaxReadRunes)
}

var (
	versionNo   = regexp.MustCompile(`(?:\bv|\bв|версия|версию|версии|№|#)\s*(\d+)`)
	versionDate = regexp.MustCompile(`(\d{1,2})[./-](\d{1,2})(?:[./-](\d{2,4}))?`)
)

// pickVersions chooses the versions (индексы, лента от старой к новой) that the
// words of a person point at: порядок («последняя», «предыдущая», «первая»),
// номер («v3», «версия 2»), дата («от 05.11»), значок («актуальная») и слова из
// описания или заметки. Подходящих может быть несколько: решает человек.
func pickVersions(vs []VersionInfo, words string) []int {
	w := strings.ToLower(words)
	if len(vs) == 0 {
		return nil
	}
	if out, ok := byOrder(vs, w); ok {
		return out
	}
	if m := versionNo.FindStringSubmatch(w); m != nil {
		if k, err := strconv.Atoi(m[1]); err == nil && k >= 1 && k <= len(vs) {
			return []int{k - 1}
		}
		return nil
	}
	if m := versionDate.FindStringSubmatch(w); m != nil {
		return indexes(vs, func(v VersionInfo) bool { return dateIs(v.Date, m[1], m[2], m[3]) })
	}
	return byWords(vs, w)
}

// byOrder resolves the words about the order or the badge; ok=false — таких слов нет.
func byOrder(vs []VersionInfo, w string) (out []int, ok bool) {
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
		return indexes(vs, func(v VersionInfo) bool { return v.Current }), true
	}
	return nil, false
}

func indexes(vs []VersionInfo, ok func(VersionInfo) bool) []int {
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
func byWords(vs []VersionInfo, w string) []int {
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
