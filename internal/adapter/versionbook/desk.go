// Package versionbook собирает документы папок окна в семейства версий с подписью
// «что нового», заметками и значками владельца. Им пользуются вкладка «Артефакты»
// окна, чат и MCP: все видят одни и те же списки (#547). Файлы документов не
// меняются; отметки лежат отдельным файлом.
package versionbook

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
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

// VersionView — версия документа в ленте семейства.
type VersionView struct {
	Path    string   `json:"path"`
	Date    string   `json:"date"`
	Size    int64    `json:"size"`
	Note    string   `json:"note"` // что нового, строит окно
	Dups    []string `json:"dups"`
	Mark    string   `json:"mark"`    // заметка владельца
	Current bool     `json:"current"` // значок «актуальная»
	hash    string
}

// FamilyView — один документ и его итерации лентой по времени.
type FamilyView struct {
	Name     string        `json:"name"`
	Versions []VersionView `json:"versions"`
	latest   time.Time
}

// Desk is the list of document families of the window folders: обход, кэш на
// минуту, подписи, отметки владельца. Безопасна для вызовов из нескольких
// горутин.
type Desk struct {
	source    func() *localfiles.Source
	marksPath string // версии-заметки и значки лежат рядом с config.json, 0600

	// walk — обход папок; по умолчанию src.Versions. Шов нужен тесту, которому
	// требуется медленный обход.
	walk func(ctx context.Context, src *localfiles.Source) ([]versions.File, error)

	mu       sync.Mutex
	src      *localfiles.Source // для какого источника собран список
	at       time.Time
	cache    []FamilyView
	marks    map[string]versionMark // отметки владельца; nil — еще не читались
	marksErr error                  // файл заметок не прочитался: записи отклоняются, чтобы не затереть его
}

// New returns a Desk over the current source of the window; marksPath — файл
// заметок и значков (0600), пустой — отметки не сохраняются.
func New(source func() *localfiles.Source, marksPath string) *Desk {
	return &Desk{source: source, marksPath: marksPath, walk: func(ctx context.Context, src *localfiles.Source) ([]versions.File, error) { return src.Versions(ctx) }}
}

// Families returns the families matching q (пустой — все, где есть что
// сравнить), свежие сверху. fresh собирает список заново.
func (d *Desk) Families(ctx context.Context, q string, fresh bool) ([]FamilyView, error) {
	src := d.source()
	if src == nil {
		return []FamilyView{}, nil
	}
	all, err := d.collect(ctx, src, fresh)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := []FamilyView{}
	for _, f := range d.withMarks(all) {
		if q == "" && len(f.Versions) < 2 && len(f.Versions[0].Dups) == 0 {
			continue
		}
		if q == "" || matches(f, q) {
			out = append(out, f)
		}
	}
	return out, nil
}

// withMarks puts the owner's notes and badges on a copy of the families: список
// из кэша общий, а отметки меняются без нового обхода.
func (d *Desk) withMarks(fams []FamilyView) []FamilyView {
	d.mu.Lock()
	defer d.mu.Unlock()
	marks := d.loadedMarks()
	out := make([]FamilyView, len(fams))
	for i, f := range fams {
		out[i] = f
		out[i].Versions = slices.Clone(f.Versions)
		for j, v := range out[i].Versions {
			m := marks[markKey(v)]
			out[i].Versions[j].Mark, out[i].Versions[j].Current = m.Note, m.Current
		}
	}
	return out
}

// loadedMarks reads the marks file once; the caller holds d.mu.
func (d *Desk) loadedMarks() map[string]versionMark {
	if d.marks == nil {
		d.marks, d.marksErr = loadMarks(d.marksPath)
	}
	return d.marks
}

// find looks the path up among the versions and the copies of the last list:
// отметить и сравнить можно только то, что окно само показало. The caller holds d.mu.
func (d *Desk) find(path string) (fam, ver int, ok bool) {
	for i, f := range d.cache {
		for j, v := range f.Versions {
			if v.Path == path || slices.Contains(v.Dups, path) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// MaxNoteRunes — длиннее заметка не нужна: это «отправлено Алексею», а не письмо.
const MaxNoteRunes = 500

// Mark saves the owner's note to a version; an empty note removes it.
func (d *Desk) Mark(path, note string) error {
	note = strings.TrimSpace(note)
	if n := len([]rune(note)); n > MaxNoteRunes {
		return fmt.Errorf("заметка длиннее %d знаков (%d)", MaxNoteRunes, n)
	}
	return d.update(path, func(marks map[string]versionMark, key string, _ []VersionView) {
		m := marks[key]
		m.Note = note
		setMark(marks, key, m)
	})
}

// SetCurrent puts the «актуальная» badge on the version; в семействе он один,
// у прежней отмеченной версии снимается.
func (d *Desk) SetCurrent(path string, on bool) error {
	return d.update(path, func(marks map[string]versionMark, key string, fam []VersionView) {
		if on {
			for _, v := range fam {
				if k := markKey(v); k != key {
					m := marks[k]
					m.Current = false
					setMark(marks, k, m)
				}
			}
		}
		m := marks[key]
		m.Current = on
		setMark(marks, key, m)
	})
}

func setMark(marks map[string]versionMark, key string, m versionMark) {
	if m.empty() {
		delete(marks, key)
		return
	}
	marks[key] = m
}

// update applies change to a copy of the marks of the version at path and saves
// the copy: при сбое записи прежние отметки остаются в памяти.
func (d *Desk) update(path string, change func(marks map[string]versionMark, key string, fam []VersionView)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	i, j, ok := d.find(path)
	if !ok {
		return errors.New("такой версии в списке нет")
	}
	if d.marksErr != nil {
		d.marks = nil // вдруг файл уже читается
	}
	next := maps.Clone(d.loadedMarks())
	if d.marksErr != nil {
		return fmt.Errorf("файл заметок не читается, заметки не тронуты: %w", d.marksErr)
	}
	change(next, markKey(d.cache[i].Versions[j]), d.cache[i].Versions)
	if err := saveMarks(d.marksPath, next); err != nil {
		return err
	}
	d.marks = next
	return nil
}

// compareLimit — сколько строк каждой версии отдает сравнение: окно не рисует десятки тысяч.
const compareLimit = 2000

// CompareLine — строка текста версии; Changed: ее нет в другой версии.
type CompareLine struct {
	Text    string `json:"t"`
	Changed bool   `json:"c"`
}

// CompareView — две версии рядом.
type CompareView struct {
	Old       []CompareLine `json:"old"`
	New       []CompareLine `json:"new"`
	Truncated bool          `json:"truncated"`
}

// Compare lays two versions side by side: a — старая, b — новая. Читает только
// версии из последнего списка; файлы не меняются.
func (d *Desk) Compare(ctx context.Context, a, b string) (CompareView, error) {
	src := d.source()
	if src == nil {
		return CompareView{}, errors.New("папки окна не подключены")
	}
	d.mu.Lock()
	famA, verA, okA := d.find(a)
	famB, verB, okB := d.find(b)
	d.mu.Unlock()
	if !okA || !okB {
		return CompareView{}, errors.New("такой версии в списке нет")
	}
	if famA != famB {
		return CompareView{}, errors.New("версии из разных документов не сравниваются")
	}
	if verA > verB {
		a, b = b, a // старая слева, как бы ни пришли аргументы
	}
	if err := ctx.Err(); err != nil {
		return CompareView{}, err
	}
	ta, err := src.VersionText(a)
	if err != nil {
		return CompareView{}, fmt.Errorf("текст старой версии недоступен: %w", err)
	}
	tb, err := src.VersionText(b)
	if err != nil {
		return CompareView{}, fmt.Errorf("текст новой версии недоступен: %w", err)
	}
	oldLines, newLines := versions.Compare(ta, tb)
	out := CompareView{Truncated: len(oldLines) > compareLimit || len(newLines) > compareLimit}
	out.Old, out.New = capLines(oldLines), capLines(newLines)
	return out, nil
}

// Text returns the text of a version of the last list: чат и вкладка читают
// только то, что окно само показало. Файлы не меняются.
func (d *Desk) Text(ctx context.Context, path string) (string, error) {
	src := d.source()
	if src == nil {
		return "", errors.New("папки окна не подключены")
	}
	d.mu.Lock()
	_, _, ok := d.find(path)
	d.mu.Unlock()
	if !ok {
		return "", errors.New("такой версии в списке нет")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return src.VersionText(path)
}

func capLines(ls []versions.Line) []CompareLine {
	out := make([]CompareLine, 0, min(len(ls), compareLimit))
	for _, l := range ls[:min(len(ls), compareLimit)] {
		out = append(out, CompareLine{Text: l.Text, Changed: l.Changed})
	}
	return out
}

func (d *Desk) collect(ctx context.Context, src *localfiles.Source, fresh bool) ([]FamilyView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !fresh && d.src == src && time.Since(d.at) < versionsTTL {
		return d.cache, nil
	}
	files, err := d.walk(ctx, src)
	if err != nil {
		return nil, err
	}
	fams := versions.Group(files)
	d.cache = make([]FamilyView, 0, len(fams))
	for _, f := range fams {
		d.cache = append(d.cache, view(f))
	}
	disambiguate(d.cache)
	sortViews(d.cache)
	d.src, d.at = src, time.Now()
	return d.cache, nil
}

// sortViews — новее первым; при равных датах по имени и пути, чтобы список не
// прыгал между обходами.
func sortViews(vs []FamilyView) {
	slices.SortFunc(vs, func(a, b FamilyView) int {
		return cmp.Or(b.latest.Compare(a.latest), cmp.Compare(a.Name, b.Name), cmp.Compare(firstPath(a), firstPath(b)))
	})
}

func firstPath(f FamilyView) string {
	if len(f.Versions) == 0 {
		return ""
	}
	return f.Versions[0].Path
}

// disambiguate подписывает папкой семейства с одинаковым именем: документы,
// разошедшиеся по тексту, называются одинаково, и без папки их не отличить.
func disambiguate(vs []FamilyView) {
	for _, withDir := range []func(string) string{filepath.Base, func(d string) string { return d }} {
		count := map[string]int{}
		for _, v := range vs {
			count[v.Name]++
		}
		for i := range vs {
			if count[vs[i].Name] > 1 && len(vs[i].Versions) > 0 {
				vs[i].Name += " (" + withDir(filepath.Dir(vs[i].Versions[0].Path)) + ")"
			}
		}
	}
}

func view(f versions.Family) FamilyView {
	v := FamilyView{Name: f.Name, Versions: make([]VersionView, 0, len(f.Versions))}
	for _, x := range f.Versions {
		when := versions.When(x.File) // та же дата, по которой версии выстроены
		v.Versions = append(v.Versions, VersionView{
			Path: x.Path, Date: when.Format("02.01.2006"), Size: x.Size, Note: noteText(x.Change), Dups: append([]string{}, x.Dups...), hash: x.Hash,
		})
		v.latest = when
	}
	return v
}

// matches — запрос в имени семейства или в любом пути, включая дубли.
func matches(f FamilyView, q string) bool {
	if strings.Contains(strings.ToLower(f.Name), q) {
		return true
	}
	for _, v := range f.Versions {
		if strings.Contains(strings.ToLower(v.Path), q) || strings.Contains(strings.ToLower(v.Mark), q) {
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
		return "текст не менялся, файл отличается (правка оформления или вложений)" + skippedNote(c)
	}
	var parts []string
	if len(c.Added) > 0 {
		parts = append(parts, fmt.Sprintf("добавлено строк: %d — %s", len(c.Added), samples(c.Added)))
	}
	if len(c.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("убрано строк: %d — %s", len(c.Removed), samples(c.Removed)))
	}
	return strings.Join(parts, "; ") + skippedNote(c)
}

// skippedNote — сравнение шло мимо версий без текста: без пометки правки
// пропущенной версии выглядели бы правками этой.
func skippedNote(c versions.Change) string {
	if c.Skipped == 0 {
		return ""
	}
	return fmt.Sprintf(" (сравнено через %d %s без текста)", c.Skipped, ruPlural(c.Skipped, "версию", "версии", "версий"))
}

func ruPlural(n int, one, few, many string) string {
	switch {
	case n%100 >= 11 && n%100 <= 14:
		return many
	case n%10 == 1:
		return one
	case n%10 >= 2 && n%10 <= 4:
		return few
	}
	return many
}

func samples(ls []string) string {
	var out []string
	for i, l := range ls {
		if i == maxSamples {
			out = append(out, fmt.Sprintf("…и еще %d", len(ls)-maxSamples))
			break
		}
		if r := []rune(l); len(r) > maxSampleLen {
			l = string(r[:maxSampleLen]) + "…"
		}
		out = append(out, "«"+l+"»")
	}
	return strings.Join(out, ", ")
}
