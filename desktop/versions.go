package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// Экран «Версии» (#547): документы папок окна собраны в семейства, у каждой
// версии подпись «что нового». Без запроса показываются только семейства, где
// есть что сравнить (две версии или дубль); запрос ищет и по одиноким файлам.

// versionsTTL — сколько держится собранный список: обход читает и хеширует
// документы, и перелистывание вкладки не должно делать это заново.
//
// ponytail: кэш в памяти на минуту, привязан к источнику. Потолок: сотни
// документов в семействах, первый вызов — секунды. Путь: кэш на диске по
// размеру и времени изменения (как extractcache), см. localfiles.Versions.
const versionsTTL = time.Minute

// versionView — версия документа в ленте семейства.
type versionView struct {
	Path string   `json:"path"`
	Date string   `json:"date"`
	Size int64    `json:"size"`
	Note string   `json:"note"`
	Dups []string `json:"dups"`
}

// familyView — один документ и его итерации лентой по времени.
type familyView struct {
	Name     string        `json:"name"`
	Versions []versionView `json:"versions"`
	latest   time.Time
}

type versionsDesk struct {
	source func() *localfiles.Source

	mu    sync.Mutex
	src   *localfiles.Source // для какого источника собран список
	at    time.Time
	cache []familyView
}

func newVersionsDesk(source func() *localfiles.Source) *versionsDesk {
	return &versionsDesk{source: source}
}

// Families returns the families matching q (пустой — все, где есть что
// сравнить), свежие сверху. fresh собирает список заново.
func (d *versionsDesk) Families(ctx context.Context, q string, fresh bool) ([]familyView, error) {
	src := d.source()
	if src == nil {
		return []familyView{}, nil
	}
	all, err := d.collect(ctx, src, fresh)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := []familyView{}
	for _, f := range all {
		if q == "" && len(f.Versions) < 2 && len(f.Versions[0].Dups) == 0 {
			continue
		}
		if q == "" || matches(f, q) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (d *versionsDesk) collect(ctx context.Context, src *localfiles.Source, fresh bool) ([]familyView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !fresh && d.src == src && time.Since(d.at) < versionsTTL {
		return d.cache, nil
	}
	files, err := src.Versions(ctx)
	if err != nil {
		return nil, err
	}
	fams := versions.Group(files)
	d.cache = make([]familyView, 0, len(fams))
	for _, f := range fams {
		d.cache = append(d.cache, view(f))
	}
	slices.SortFunc(d.cache, func(a, b familyView) int { return b.latest.Compare(a.latest) })
	d.src, d.at = src, time.Now()
	return d.cache, nil
}

func view(f versions.Family) familyView {
	v := familyView{Name: f.Name, Versions: make([]versionView, 0, len(f.Versions))}
	for _, x := range f.Versions {
		when := versions.When(x.File) // та же дата, по которой версии выстроены
		v.Versions = append(v.Versions, versionView{
			Path: x.Path, Date: when.Format("02.01.2006"), Size: x.Size, Note: noteText(x.Change), Dups: append([]string{}, x.Dups...),
		})
		v.latest = when
	}
	return v
}

// matches — запрос в имени семейства или в любом пути, включая дубли.
func matches(f familyView, q string) bool {
	if strings.Contains(strings.ToLower(f.Name), q) {
		return true
	}
	for _, v := range f.Versions {
		if strings.Contains(strings.ToLower(v.Path), q) {
			return true
		}
		if slices.ContainsFunc(v.Dups, func(p string) bool { return strings.Contains(strings.ToLower(p), q) }) {
			return true
		}
	}
	return false
}

// Для описания «что нового»: сколько примеров строк и какой длины показывать.
const (
	maxSamples   = 3
	maxSampleLen = 60
)

// noteText — описание версии словами окна: ядро (versions) отдает вид
// изменения и строки, формулировки живут здесь.
func noteText(c versions.Change) string {
	switch c.Kind {
	case versions.KindFirst:
		return "первая версия"
	case versions.KindNewFormat:
		return "новый формат: " + c.Format
	case versions.KindNoText:
		return "текст недоступен, сравнить нельзя"
	case versions.KindTooBig:
		return "слишком большой, не читался"
	case versions.KindLayoutOnly:
		return "текст не менялся, файл отличается (правка оформления или вложений)"
	}
	var parts []string
	if len(c.Added) > 0 {
		parts = append(parts, fmt.Sprintf("добавлено %d: %s", len(c.Added), samples(c.Added)))
	}
	if len(c.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("убрано %d: %s", len(c.Removed), samples(c.Removed)))
	}
	return strings.Join(parts, "; ")
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
