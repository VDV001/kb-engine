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

// stem — имя без расширения, дат, номеров версий и слов «копия/финал»:
// ключ семейства. Имя целиком из такого шума («2026-11-10.docx», «v2.pptx»)
// остается собой: пустой ключ склеил бы чужие документы в одно семейство.
func stem(path string) string {
	whole := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	name := stripDates(whole, isoDate, func(m []string) (time.Time, bool) { return parseDate(m[1], m[2], m[3]) })
	name = stripDates(name, dmyDate, func(m []string) (time.Time, bool) { return parseDate(m[3], m[2], m[1]) })
	name = parenNum.ReplaceAllString(name, " ")
	var keep []string
	for w := range strings.SplitSeq(separator.ReplaceAllString(name, " "), " ") {
		if w != "" && !noise[w] && !versionNo.MatchString(w) {
			keep = append(keep, w)
		}
	}
	if len(keep) == 0 {
		return whole
	}
	return strings.Join(keep, " ")
}

// stripDates blanks the matches of re that are real calendar dates: 31.02 —
// не дата и в ключе остается.
func stripDates(name string, re *regexp.Regexp, parse func(m []string) (time.Time, bool)) string {
	return re.ReplaceAllStringFunc(name, func(match string) string {
		if _, ok := parse(re.FindStringSubmatch(match)); ok {
			return " "
		}
		return match
	})
}

// when — дата версии: из имени, если она там есть, иначе время изменения.
func when(f File) time.Time {
	base := filepath.Base(f.Path)
	if m := isoDate.FindStringSubmatch(base); m != nil {
		if t, ok := parseDate(m[1], m[2], m[3]); ok {
			return t
		}
	}
	if m := dmyDate.FindStringSubmatch(base); m != nil {
		if t, ok := parseDate(m[3], m[2], m[1]); ok {
			return t
		}
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

// Group собирает файлы в семейства: одно имя без шума — одно семейство, в любой
// папке и формате. Байт-в-байт копии (по хешу) уходят в Dups самой ранней
// версии и версиями не считаются.
func Group(files []File) []Family {
	byStem := map[string][]File{}
	for _, f := range files {
		k := stem(f.Path)
		byStem[k] = append(byStem[k], f)
	}
	fams := make([]Family, 0, len(byStem))
	for _, fs := range byStem {
		fams = append(fams, family(fs))
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
	name := strings.TrimSuffix(filepath.Base(vs[0].Path), filepath.Ext(vs[0].Path))
	return Family{Name: name, Versions: vs}
}

// change описывает версию относительно предыдущей версии того же формата.
func change(before []Version, v Version) Change {
	var prev *Version
	for i, b := range slices.Backward(before) {
		if ext(b.Path) == ext(v.Path) {
			prev = &before[i]
			break
		}
	}
	switch {
	case prev == nil && len(before) == 0:
		return Change{Kind: KindFirst}
	case prev == nil:
		return Change{Kind: KindNewFormat, Format: ext(v.Path)}
	case prev.Text == "" || v.Text == "":
		return Change{Kind: KindNoText}
	}
	added, removed := lineDiff(prev.Text, v.Text)
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
func lineDiff(oldText, newText string) (added, removed []string) {
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
	return added, removed
}
