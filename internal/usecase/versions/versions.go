// Package versions собирает файлы в семейства — итерации одного документа —
// и подписывает каждую версию: что это за версия и что нового относительно
// предыдущей. Чистое ядро без диска: файлы приходят с хешем и текстом.
package versions

import (
	"cmp"
	"fmt"
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

// Version — версия в семействе с подписью «что нового».
type Version struct {
	File
	Dups []string // пути байт-в-байт копий
	Note string
}

// Family — итерации одного документа лентой по времени.
type Family struct {
	Name     string
	Versions []Version
}

// Для подписи «что нового»: сколько примеров строк и какой длины показывать.
const (
	maxSamples   = 3
	maxSampleLen = 60
)

var (
	// \b в Go не знает кириллицы, поэтому имя режется на слова по разделителям.
	isoDate   = regexp.MustCompile(`(\d{4})[-_.](\d{1,2})[-_.](\d{1,2})`)
	dmyDate   = regexp.MustCompile(`(\d{1,2})[-_.](\d{1,2})[-_.](\d{4})`)
	parenNum  = regexp.MustCompile(`\(\d+\)`)
	separator = regexp.MustCompile(`[\s_.\-]+`)
	versionNo = regexp.MustCompile(`^v\d+$`)
	noise     = map[string]bool{"копия": true, "copy": true, "финал": true, "final": true}
)

// stem — имя без расширения, дат, номеров версий и слов «копия/финал»:
// ключ семейства.
func stem(path string) string {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	name = isoDate.ReplaceAllString(name, " ")
	name = dmyDate.ReplaceAllString(name, " ")
	name = parenNum.ReplaceAllString(name, " ")
	var keep []string
	for w := range strings.SplitSeq(separator.ReplaceAllString(name, " "), " ") {
		if w != "" && !noise[w] && !versionNo.MatchString(w) {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, " ")
}

// when — дата версии: из имени, если она там есть, иначе время изменения.
func when(f File) time.Time {
	base := filepath.Base(f.Path)
	if m := isoDate.FindStringSubmatch(base); m != nil {
		return date(m[1], m[2], m[3], f.Mod)
	}
	if m := dmyDate.FindStringSubmatch(base); m != nil {
		return date(m[3], m[2], m[1], f.Mod)
	}
	return f.Mod
}

// When — дата версии: из имени, иначе время изменения. Её же показывает окно.
func When(f File) time.Time { return when(f) }

func date(y, mo, d string, fallback time.Time) time.Time {
	yi, _ := strconv.Atoi(y)
	mi, _ := strconv.Atoi(mo)
	di, _ := strconv.Atoi(d)
	t := time.Date(yi, time.Month(mi), di, 0, 0, 0, 0, time.UTC)
	if t.Year() != yi || int(t.Month()) != mi || t.Day() != di {
		return fallback // 31.02 и подобное — не дата
	}
	return t
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
		vs[i].Note = note(vs[:i], vs[i])
	}
	name := strings.TrimSuffix(filepath.Base(vs[0].Path), filepath.Ext(vs[0].Path))
	return Family{Name: name, Versions: vs}
}

// note подписывает версию относительно предыдущей версии того же формата.
func note(before []Version, v Version) string {
	var prev *Version
	for i, b := range slices.Backward(before) {
		if ext(b.Path) == ext(v.Path) {
			prev = &before[i]
			break
		}
	}
	switch {
	case prev == nil && len(before) == 0:
		return "первая версия"
	case prev == nil:
		return "новый формат: " + ext(v.Path)
	case prev.Text == "" || v.Text == "":
		return "текст недоступен, сравнить нельзя"
	}
	added, removed := lineDiff(prev.Text, v.Text)
	if len(added) == 0 && len(removed) == 0 {
		return "текст не менялся, файл отличается (правка оформления или вложений)"
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("добавлено %d: %s", len(added), samples(added)))
	}
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("убрано %d: %s", len(removed), samples(removed)))
	}
	return strings.Join(parts, "; ")
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
	for _, l := range n {
		if !slices.Contains(o, l) {
			added = append(added, l)
		}
	}
	for _, l := range o {
		if !slices.Contains(n, l) {
			removed = append(removed, l)
		}
	}
	return added, removed
}

func samples(ls []string) string {
	var out []string
	for i, l := range ls {
		if i == maxSamples {
			out = append(out, fmt.Sprintf("…и ещё %d", len(ls)-maxSamples))
			break
		}
		if r := []rune(l); len(r) > maxSampleLen {
			l = string(r[:maxSampleLen]) + "…"
		}
		out = append(out, "«"+l+"»")
	}
	return strings.Join(out, ", ")
}
