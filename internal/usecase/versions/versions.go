// Package versions собирает файлы в семейства — итерации одного документа —
// и подписывает каждую версию: что это за версия и что нового относительно
// предыдущей. Чистое ядро без диска: файлы приходят с хешем и текстом.
package versions

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// File — один файл на входе: хеш и текст считает вызывающий.
type File struct {
	Path string
	Size int64
	Mod  time.Time
	Hash string // SHA-256; пусто — не считался
	Text string // пусто — текст недоступен
	// TooBig — файл больше предела чтения: он в списке, но не читался и не хешировался.
	TooBig bool
}

// Kind — что произошло с версией относительно предыдущей. Слова для человека
// подбирает окно: ядро отдает вид и данные, а не готовый текст.
type Kind int

// Виды изменения версии.
const (
	KindFirst      Kind = iota + 1 // первая версия семейства
	KindNewFormat                  // первая в своем формате; Format назван
	KindNoText                     // текста нет у одной из версий, сравнить нельзя
	KindLayoutOnly                 // текст тот же, файл другой: правка оформления или вложений
	KindDiff                       // Added и Removed — строки, что появились и что ушли
	KindTooBig                     // файл больше предела чтения, не сравнивался
)

// Change — что нового в версии относительно предыдущей того же формата.
type Change struct {
	Kind    Kind
	Format  string // расширение без точки, для KindNewFormat
	Added   []string
	Removed []string
}

// Version — версия в семействе с описанием «что нового».
type Version struct {
	File
	Dups   []string // пути байт-в-байт копий
	Change Change
}

// Family — итерации одного документа лентой по времени.
type Family struct {
	Name     string
	Versions []Version
}

var (
	// \b в Go не знает кириллицы, поэтому имя режется на слова по разделителям.
	// Год 19xx или 20xx: «1000_12_5» и «2_3_1500» — номера, а не даты.
	isoDate   = regexp.MustCompile(`((?:19|20)\d{2})[-_.](\d{1,2})[-_.](\d{1,2})`)
	dmyDate   = regexp.MustCompile(`(\d{1,2})[-_.](\d{1,2})[-_.]((?:19|20)\d{2})`)
	parenNum  = regexp.MustCompile(`\(\d+\)`)
	separator = regexp.MustCompile(`[\s_.\-]+`)
	versionNo = regexp.MustCompile(`^v\d+$`)
	noise     = map[string]bool{"копия": true, "copy": true, "финал": true, "final": true}
)

// display — имя без расширения, дат, номеров версий и слов «копия/финал», с
// сохраненным регистром: им подписано семейство. Имя целиком из такого шума
// («2026-11-10.docx», «v2.pptx») остается собой: пустое имя склеило бы чужие
// документы в одно семейство.
func display(path string) string {
	whole := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name := stripDates(whole, isoDate, func(m []string) (time.Time, bool) { return parseDate(m[1], m[2], m[3]) })
	name = stripDates(name, dmyDate, func(m []string) (time.Time, bool) { return parseDate(m[3], m[2], m[1]) })
	name = parenNum.ReplaceAllString(name, " ")
	var keep []string
	for w := range strings.SplitSeq(separator.ReplaceAllString(name, " "), " ") {
		if lw := strings.ToLower(w); w != "" && !noise[lw] && !versionNo.MatchString(lw) {
			keep = append(keep, w)
		}
	}
	if len(keep) == 0 {
		return whole
	}
	return strings.Join(keep, " ")
}

// stem — ключ семейства: display без регистра.
func stem(path string) string { return strings.ToLower(display(path)) }

// dateSpan — настоящая календарная дата в имени: где она стоит и какой день.
type dateSpan struct {
	start, end int
	day        time.Time
}

// dates находит в имени даты по re. Дата отдельная: цифра вплотную к ней
// значит, что это кусок номера («0220.05.2026» — не 20.05.2026), а 31.02 —
// не дата вовсе.
func dates(name string, re *regexp.Regexp, parse func(m []string) (time.Time, bool)) []dateSpan {
	var out []dateSpan
	for _, ix := range re.FindAllStringSubmatchIndex(name, -1) {
		start, end := ix[0], ix[1]
		if start > 0 && isDigit(name[start-1]) || end < len(name) && isDigit(name[end]) {
			continue
		}
		sub := make([]string, len(ix)/2)
		for i := range sub {
			sub[i] = name[ix[2*i]:ix[2*i+1]]
		}
		if day, ok := parse(sub); ok {
			out = append(out, dateSpan{start, end, day})
		}
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// stripDates blanks the dates of name that re finds.
func stripDates(name string, re *regexp.Regexp, parse func(m []string) (time.Time, bool)) string {
	var b strings.Builder
	last := 0
	for _, d := range dates(name, re, parse) {
		b.WriteString(name[last:d.start])
		b.WriteByte(' ')
		last = d.end
	}
	b.WriteString(name[last:])
	return b.String()
}

// when — дата версии: из имени, если она там есть, иначе время изменения.
func when(f File) time.Time {
	base := filepath.Base(f.Path)
	if d := dates(base, isoDate, func(m []string) (time.Time, bool) { return parseDate(m[1], m[2], m[3]) }); len(d) > 0 {
		return d[0].day
	}
	if d := dates(base, dmyDate, func(m []string) (time.Time, bool) { return parseDate(m[3], m[2], m[1]) }); len(d) > 0 {
		return d[0].day
	}
	return f.Mod
}

// When — дата версии: из имени, иначе время изменения. Её же показывает окно.
func When(f File) time.Time { return when(f) }

// parseDate reports whether y-mo-d is a calendar date (31.02 — нет).
func parseDate(y, mo, d string) (time.Time, bool) {
	yi, _ := strconv.Atoi(y)
	mi, _ := strconv.Atoi(mo)
	di, _ := strconv.Atoi(d)
	t := time.Date(yi, time.Month(mi), di, 0, 0, 0, 0, time.UTC)
	return t, t.Year() == yi && int(t.Month()) == mi && t.Day() == di
}

// Key — ключ семейства: файлы с одним ключом — итерации одного документа.
func Key(path string) string { return stem(path) }

func ext(path string) string { return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".") }

// Group собирает файлы в семейства: одно имя без шума — кандидат в одно
// семейство, в любой папке и формате; тексты решают, один это документ или
// несколько (split). Байт-в-байт копии (по хешу) уходят в Dups самой ранней
// версии и версиями не считаются.
func Group(files []File) []Family {
	byStem := map[string][]File{}
	for _, f := range files {
		k := stem(f.Path)
		byStem[k] = append(byStem[k], f)
	}
	fams := make([]Family, 0, len(byStem))
	for _, fs := range byStem {
		for _, doc := range split(fs) {
			fams = append(fams, family(doc))
		}
	}
	slices.SortFunc(fams, func(a, b Family) int {
		return cmp.Or(when(a.Versions[0].File).Compare(when(b.Versions[0].File)), cmp.Compare(a.Name, b.Name))
	})
	return fams
}

func family(fs []File) Family {
	slices.SortFunc(fs, func(a, b File) int {
		return cmp.Or(when(a).Compare(when(b)), cmp.Compare(a.Path, b.Path))
	})
	var vs []Version
	byHash := map[string]int{} // хеш → индекс версии
	for _, f := range fs {
		if i, ok := byHash[f.Hash]; ok && f.Hash != "" {
			vs[i].Dups = append(vs[i].Dups, f.Path)
			continue
		}
		byHash[f.Hash] = len(vs)
		vs = append(vs, Version{File: f})
	}
	for i := range vs {
		vs[i].Change = change(vs[:i], vs[i])
	}
	return Family{Name: display(vs[0].Path), Versions: vs}
}

// Один документ или два: у длинных текстов (от minJudgedLines разных строк с
// обеих сторон) общей должна быть хотя бы доля sameDocShare от меньшего текста.
// Меньшего, а не большего: урезанная спецификация остается собой. Короткие
// тексты не судим: у записки в три строки правка и есть почти весь текст.
const (
	sameDocShare   = 0.3
	minJudgedLines = 10
)

// textSet — разные непустые строки текста.
type textSet map[string]struct{}

// texts кэширует наборы строк по пути: каждый файл разбирается один раз,
// сколько бы документов с ним ни сравнивали.
type texts map[string]textSet

func (c texts) of(f File) textSet {
	if s, ok := c[f.Path]; ok {
		return s
	}
	s := textSet{}
	for _, l := range lines(f.Text) {
		s[l] = struct{}{}
	}
	c[f.Path] = s
	return s
}

// share — доля общих строк от меньшего из двух наборов; judged=false: один из
// них короче minJudgedLines, судить рано.
func share(a, b textSet) (frac float64, judged bool) {
	if len(a) < minJudgedLines || len(b) < minJudgedLines {
		return 0, false
	}
	if len(b) < len(a) {
		a, b = b, a
	}
	n := 0
	for l := range a {
		if _, ok := b[l]; ok {
			n++
		}
	}
	return float64(n) / float64(len(a)), true
}

// split разводит файлы одного имени по документам. Файлы идут по времени; каждый
// садится к документу, на который похож текстом, иначе открывает новый.
func split(fs []File) [][]File {
	slices.SortFunc(fs, func(a, b File) int {
		return cmp.Or(when(a).Compare(when(b)), cmp.Compare(a.Path, b.Path))
	})
	var docs [][]File
	tx := texts{}
	for _, f := range fs {
		if i := home(docs, f, tx); i >= 0 {
			docs[i] = append(docs[i], f)
		} else {
			docs = append(docs, []File{f})
		}
	}
	return docs
}

// home — индекс документа, к которому относится f, или -1 (новый документ).
//
// Копия по хешу идет к оригиналу. Дальше f сравнивается со всеми версиями
// документа в том же формате (откат к старой правке находит свое семейство):
// у PDF строки рвутся иначе, чем у docx, и сравнение через формат разводило бы
// один документ. Где судить не из чего (нет текста, короткий текст, нет версии
// в этом формате), f не открывает новый документ: сначала садится к тому из
// «неясных», на который похож текстом в любом формате, иначе — по соседству
// (nearest).
func home(docs [][]File, f File, tx texts) int {
	if i := byHash(docs, f); i >= 0 {
		return i
	}
	if f.Text == "" {
		all := make([]int, len(docs))
		for i := range docs {
			all[i] = i
		}
		return nearest(docs, all, f)
	}
	mine := tx.of(f)
	best, bestShare := -1, 0.0
	var unsure []int
	for i, d := range docs {
		frac, judged := bestShareIn(d, f, mine, tx, true)
		switch {
		case !judged:
			unsure = append(unsure, i)
		case frac >= sameDocShare && frac > bestShare:
			best, bestShare = i, frac
		}
	}
	if best >= 0 {
		return best
	}
	if i := resembling(docs, unsure, f, mine, tx); i >= 0 {
		return i
	}
	return nearest(docs, unsure, f)
}

// resembling — документ из among, на который f похож текстом в любом формате
// (долей не меньше sameDocShare), или -1. Формат разный: этого признака хватает,
// чтобы присоединить файл, но не чтобы открыть новый документ.
func resembling(docs [][]File, among []int, f File, mine textSet, tx texts) int {
	best, bestShare := -1, 0.0
	for _, i := range among {
		if frac, judged := bestShareIn(docs[i], f, mine, tx, false); judged && frac >= sameDocShare && frac > bestShare {
			best, bestShare = i, frac
		}
	}
	return best
}

// byHash — документ, где уже лежит файл с тем же хешем, или -1.
func byHash(docs [][]File, f File) int {
	if f.Hash == "" {
		return -1
	}
	return slices.IndexFunc(docs, func(d []File) bool {
		return slices.ContainsFunc(d, func(m File) bool { return m.Hash == f.Hash })
	})
}

// bestShareIn — наибольшая доля общих строк f с версиями документа d (только
// того же формата, если sameFormat); judged=false, если сравнить было не с чем.
func bestShareIn(d []File, f File, mine textSet, tx texts, sameFormat bool) (best float64, judged bool) {
	for _, m := range d {
		if m.Text == "" || sameFormat && ext(m.Path) != ext(f.Path) {
			continue
		}
		if frac, ok := share(mine, tx.of(m)); ok {
			best, judged = max(best, frac), true
		}
	}
	return best, judged
}

// nearest — документ из among, к которому f ближе всего: сначала тот, у кого
// есть файл в той же папке, затем тот, чья последняя версия новее. -1, если among пуст.
//
// ponytail: подсказок два — папка и дата. Потолок: два одноимённых документа
// в одной папке, а файл без текста и без общих строк с ними. Путь: имя
// родительской папки и размер.
func nearest(docs [][]File, among []int, f File) int {
	best, bestSame, bestAt := -1, false, time.Time{}
	for _, i := range among {
		d := docs[i]
		same := slices.ContainsFunc(d, func(m File) bool { return filepath.Dir(m.Path) == filepath.Dir(f.Path) })
		at := when(d[len(d)-1])
		if best < 0 || same && !bestSame || same == bestSame && !at.Before(bestAt) {
			best, bestSame, bestAt = i, same, at
		}
	}
	return best
}

// change описывает версию относительно предыдущей версии того же формата.
func change(before []Version, v Version) Change {
	if v.TooBig {
		return Change{Kind: KindTooBig}
	}
	// Сравнивается с последней версией того же формата, у которой есть текст:
	// слишком большая или нечитаемая версия посередине ленты ее не заслоняет.
	var prev *Version
	sameFormat := false
	for i, b := range slices.Backward(before) {
		if ext(b.Path) != ext(v.Path) {
			continue
		}
		sameFormat = true
		if b.Text != "" {
			prev = &before[i]
			break
		}
	}
	switch {
	case !sameFormat && len(before) == 0:
		return Change{Kind: KindFirst}
	case !sameFormat:
		return Change{Kind: KindNewFormat, Format: ext(v.Path)}
	case prev == nil || v.Text == "":
		return Change{Kind: KindNoText}
	}
	return textChange(prev.Text, v.Text)
}

// textChange сравнивает тексты двух версий одного формата.
func textChange(oldText, newText string) Change {
	added, removed, _, _ := lineDiff(oldText, newText)
	if len(added) == 0 && len(removed) == 0 {
		return Change{Kind: KindLayoutOnly}
	}
	return Change{Kind: KindDiff, Added: added, Removed: removed}
}

func lines(text string) []string {
	var out []string
	for l := range strings.Lines(text) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// lineDiff — строки, которых нет в другой версии. Порядок и повторы не важны.
//
// ponytail: сравнение по множеству строк — перестановка абзацев не видна.
// Потолок: «поменяли местами два слайда». Путь: LCS по строкам.
func lineDiff(oldText, newText string) (added, removed []string, oldN, newN int) {
	o, n := lines(oldText), lines(newText)
	inOld, inNew := make(map[string]bool, len(o)), make(map[string]bool, len(n))
	for _, l := range o {
		inOld[l] = true
	}
	for _, l := range n {
		inNew[l] = true
	}
	for _, l := range n {
		if !inOld[l] {
			added = append(added, l)
		}
	}
	for _, l := range o {
		if !inNew[l] {
			removed = append(removed, l)
		}
	}
	return added, removed, len(o), len(n)
}

// Line — строка текста версии в сравнении двух версий.
type Line struct {
	Text    string
	Changed bool // строки нет в другой версии
}

// Compare раскладывает тексты двух версий по строкам и помечает те, которых
// нет в другой версии. Тот же счет, что у подписи «что нового»: порядок и
// повторы строк не учитываются.
func Compare(oldText, newText string) (oldLines, newLines []Line) {
	return nil, nil
}
