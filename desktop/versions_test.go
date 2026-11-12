package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/versions"
)

func docxFile(t *testing.T, path string, paras ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, p := range paras {
		b.WriteString(`<w:p><w:r><w:t>` + p + `</w:t></w:r></w:p>`)
	}
	fmt.Fprintf(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>%s</w:body></w:document>`, b.String())
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func versionsDeskOver(t *testing.T, root string) *versionsDesk {
	t.Helper()
	src, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return newVersionsDesk(func() *localfiles.Source { return src }, filepath.Join(t.TempDir(), "version-marks.json"))
}

// restarted — новое окно над той же папкой и тем же файлом заметок.
func restarted(t *testing.T, root string, old *versionsDesk) *versionsDesk {
	t.Helper()
	src, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return newVersionsDesk(func() *localfiles.Source { return src }, old.marksPath)
}

func TestVersions_FamiliesShowTheRibbonAndSkipLoneFiles(t *testing.T) {
	root := t.TempDir()
	docxFile(t, filepath.Join(root, "p", "КП_2026_11_05.docx"), "Цена 100")
	docxFile(t, filepath.Join(root, "p", "КП_2026_11_10.docx"), "Цена 100", "Гарантия")
	docxFile(t, filepath.Join(root, "p", "Одинокий.docx"), "Текст")
	d := versionsDeskOver(t, root)

	got, err := d.Families(context.Background(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Versions) != 2 {
		t.Fatalf("ждали одно семейство из двух версий: %+v", got)
	}
	vs := got[0].Versions
	if vs[0].Note != "первая версия" || !strings.Contains(vs[1].Note, "Гарантия") || vs[0].Date == "" || vs[0].Size == 0 {
		t.Errorf("лента: %+v", vs)
	}

	found, err := d.Families(context.Background(), "одинок", false)
	if err != nil || len(found) != 1 || found[0].Versions[0].Path != filepath.Join(root, "p", "Одинокий.docx") {
		t.Errorf("поиск по имени находит и одинокий файл: %+v %v", found, err)
	}
}

func TestVersions_CopyShownAsDuplicate(t *testing.T) {
	root := t.TempDir()
	orig := filepath.Join(root, "p", "Смета.docx")
	docxFile(t, orig, "Строка")
	b, _ := os.ReadFile(orig)
	cp := filepath.Join(root, "Загрузки", "Смета (1).docx")
	_ = os.MkdirAll(filepath.Dir(cp), 0o755)
	if err := os.WriteFile(cp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := versionsDeskOver(t, root).Families(context.Background(), "", true)
	if err != nil || len(got) != 1 || len(got[0].Versions) != 1 || len(got[0].Versions[0].Dups) != 1 {
		t.Fatalf("дубль: %+v %v", got, err)
	}
}

func TestVersions_NoSourceIsEmpty(t *testing.T) {
	d := newVersionsDesk(func() *localfiles.Source { return nil }, "")
	got, err := d.Families(context.Background(), "", true)
	if err != nil || len(got) != 0 {
		t.Errorf("без источника: %+v %v", got, err)
	}
}

func TestVersions_ShownDateFollowsTheNameNotTheCopyTime(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "КП_2026_11_05.docx"), filepath.Join(root, "КП_2026_11_10.docx")
	docxFile(t, a, "Цена")
	docxFile(t, b, "Цена", "Гарантия")
	// копия старой версии свежее новой: по времени изменения порядок был бы обратным
	now := time.Now()
	_ = os.Chtimes(a, now, now)
	_ = os.Chtimes(b, now.Add(-72*time.Hour), now.Add(-72*time.Hour))
	got, err := versionsDeskOver(t, root).Families(context.Background(), "", true)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	vs := got[0].Versions
	if vs[0].Date != "05.11.2026" || vs[1].Date != "10.11.2026" {
		t.Errorf("даты: %q, %q", vs[0].Date, vs[1].Date)
	}
}

func TestNoteText(t *testing.T) {
	long := strings.Repeat("я", 70)
	for _, c := range []struct {
		name string
		in   versions.Change
		want string
	}{
		{"первая", versions.Change{Kind: versions.KindFirst}, "первая версия"},
		{"формат", versions.Change{Kind: versions.KindNewFormat, Format: "pdf"}, "новый формат: pdf"},
		{"без текста", versions.Change{Kind: versions.KindNoText}, "текст недоступен, сравнить нельзя"},
		{"оформление", versions.Change{Kind: versions.KindLayoutOnly}, "текст не менялся, файл отличается (правка оформления или вложений)"},
		{"большой", versions.Change{Kind: versions.KindTooBig}, "слишком большой, не читался"},
		{"дифф", versions.Change{Kind: versions.KindDiff, Added: []string{"a", "b"}, Removed: []string{"c"}}, "добавлено строк: 2 — «a», «b»; убрано строк: 1 — «c»"},
		{"много и длинно", versions.Change{Kind: versions.KindDiff, Added: []string{long, "b", "c", "d", "e"}}, "добавлено строк: 5 — «" + strings.Repeat("я", 60) + "…», «b», «c», …и еще 2"},
	} {
		if got := noteText(c.in); got != c.want {
			t.Errorf("%s: %q, ждали %q", c.name, got, c.want)
		}
	}
}

func TestSortViews_EqualDatesKeepAStableOrder(t *testing.T) {
	d := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	a := []familyView{{Name: "Б", latest: d}, {Name: "А", latest: d}, {Name: "В", latest: d.AddDate(0, 0, 1)}}
	b := []familyView{a[1], a[2], a[0]}
	sortViews(a)
	sortViews(b)
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Fatalf("порядок зависит от входа: %v и %v", a, b)
		}
	}
	if a[0].Name != "В" || a[1].Name != "А" {
		t.Errorf("новее первым, при равных датах по имени: %v", a)
	}
}

// kp кладет две версии КП и возвращает окно, пути версий и семейство.
func kp(t *testing.T) (root string, d *versionsDesk, v1, v2 string) {
	t.Helper()
	root = t.TempDir()
	v1, v2 = filepath.Join(root, "p", "КП_2026_11_05.docx"), filepath.Join(root, "p", "КП_2026_11_10.docx")
	docxFile(t, v1, "Цена 100", "Срок 7 дней")
	docxFile(t, v2, "Цена 100", "Срок 5 дней", "Гарантия")
	d = versionsDeskOver(t, root)
	if _, err := d.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	return root, d, v1, v2
}

func versionsOf(t *testing.T, d *versionsDesk) []versionView {
	t.Helper()
	got, err := d.Families(context.Background(), "", false)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	return got[0].Versions
}

func TestMark_NoteSurvivesRestartAndRename(t *testing.T) {
	root, d, v1, _ := kp(t)
	if err := d.Mark(v1, "  отправлено Алексею  "); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(root, "p", "КП_2026_11_05 (1).docx")
	if err := os.Rename(v1, renamed); err != nil {
		t.Fatal(err)
	}
	d2 := restarted(t, root, d)
	if _, err := d2.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	vs := versionsOf(t, d2)
	if vs[0].Mark != "отправлено Алексею" {
		t.Errorf("заметка не пережила перезапуск и переименование файла: %+v", vs)
	}
	if vs[1].Mark != "" {
		t.Errorf("заметка прилипла к соседней версии: %+v", vs)
	}
}

func TestMark_EmptyNoteRemovesItAndLongOnesAreRefused(t *testing.T) {
	_, d, v1, _ := kp(t)
	if err := d.Mark(v1, "заметка"); err != nil {
		t.Fatal(err)
	}
	if err := d.Mark(v1, "   "); err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(t, d)[0].Mark; got != "" {
		t.Errorf("пустая заметка должна убрать прежнюю: %q", got)
	}
	if err := d.Mark(v1, strings.Repeat("я", 501)); err == nil {
		t.Error("заметка длиннее 500 знаков должна быть отклонена")
	}
}

func TestMark_OnlyForKnownVersions(t *testing.T) {
	root, d, _, _ := kp(t)
	other := filepath.Join(root, "p", "Другой.docx")
	docxFile(t, other, "x")
	for name, p := range map[string]string{"вне списка": other, "вне папок": "/etc/hosts", "относительный": "КП.docx"} {
		if err := d.Mark(p, "заметка"); err == nil {
			t.Errorf("%s: заметка принята", name)
		}
		if err := d.SetCurrent(p, true); err == nil {
			t.Errorf("%s: значок принят", name)
		}
	}
}

func TestSetCurrent_OneBadgePerFamily(t *testing.T) {
	_, d, v1, v2 := kp(t)
	if err := d.SetCurrent(v1, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetCurrent(v2, true); err != nil {
		t.Fatal(err)
	}
	vs := versionsOf(t, d)
	if vs[0].Current || !vs[1].Current {
		t.Errorf("значок должен быть у одной версии, последней отмеченной: %+v", vs)
	}
	if err := d.SetCurrent(v2, false); err != nil {
		t.Fatal(err)
	}
	if vs = versionsOf(t, d); vs[0].Current || vs[1].Current {
		t.Errorf("значок не снялся: %+v", vs)
	}
}

func TestMark_CopyShareTheNoteOfTheOriginal(t *testing.T) {
	root := t.TempDir()
	orig := filepath.Join(root, "p", "Смета.docx")
	docxFile(t, orig, "Строка")
	b, _ := os.ReadFile(orig)
	cp := filepath.Join(root, "Загрузки", "Смета (1).docx")
	_ = os.MkdirAll(filepath.Dir(cp), 0o755)
	if err := os.WriteFile(cp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	d := versionsDeskOver(t, root)
	if _, err := d.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	if err := d.Mark(cp, "актуальная смета"); err != nil {
		t.Fatalf("заметка к дублю: %v", err)
	}
	if got := versionsOf(t, d)[0].Mark; got != "актуальная смета" {
		t.Errorf("дубль и оригинал — одно содержимое, заметка общая: %q", got)
	}
}

func TestMark_FoundBySearchAndBrokenFileIsIgnored(t *testing.T) {
	root, d, v1, _ := kp(t)
	if err := d.Mark(v1, "отправлено Алексею"); err != nil {
		t.Fatal(err)
	}
	found, err := d.Families(context.Background(), "алексею", false)
	if err != nil || len(found) != 1 {
		t.Errorf("поиск по заметке: %+v %v", found, err)
	}
	info, err := os.Stat(d.marksPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("файл заметок: %v %v (ждали 0600)", info, err)
	}
	if err := os.WriteFile(d.marksPath, []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d2 := restarted(t, root, d)
	if _, err := d2.Families(context.Background(), "", true); err != nil {
		t.Errorf("битый файл заметок не должен ломать список: %v", err)
	}
}

func TestCompare_SideBySideMarksChangedLines(t *testing.T) {
	_, d, v1, v2 := kp(t)
	got, err := d.Compare(context.Background(), v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Old) != 2 || !got.Old[1].Changed || got.Old[0].Changed {
		t.Errorf("старая версия: %+v", got.Old)
	}
	if len(got.New) != 3 || got.New[0].Changed || !got.New[1].Changed || !got.New[2].Changed || got.Truncated {
		t.Errorf("новая версия: %+v", got.New)
	}
}

func TestCompare_RefusesUnknownPathsAndCapsHugeTexts(t *testing.T) {
	root, d, v1, _ := kp(t)
	if _, err := d.Compare(context.Background(), v1, "/etc/hosts"); err == nil {
		t.Error("сравнение с чужим файлом принято")
	}
	big1, big2 := filepath.Join(root, "p", "Большой_2026_11_05.docx"), filepath.Join(root, "p", "Большой_2026_11_10.docx")
	var paras []string
	for i := range compareLimit + 100 {
		paras = append(paras, fmt.Sprintf("строка %d", i))
	}
	docxFile(t, big1, paras...)
	docxFile(t, big2, append(paras, "новая")...)
	if _, err := d.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	got, err := d.Compare(context.Background(), big1, big2)
	if err != nil || !got.Truncated || len(got.Old) != compareLimit || len(got.New) != compareLimit {
		t.Errorf("большой текст: %d/%d строк, truncated=%v, %v", len(got.Old), len(got.New), got.Truncated, err)
	}
}

func TestNoteText_SaysWhenUnreadableVersionsWereSkipped(t *testing.T) {
	got := noteText(versions.Change{Kind: versions.KindDiff, Added: []string{"a"}, Skipped: 2})
	if !strings.Contains(got, "через 2 версии без текста") {
		t.Errorf("подпись не говорит о пропущенных версиях: %q", got)
	}
	if got := noteText(versions.Change{Kind: versions.KindDiff, Added: []string{"a"}}); strings.Contains(got, "без текста") {
		t.Errorf("лишняя пометка: %q", got)
	}
}

func TestDisambiguate_SameNameGetsItsFolder(t *testing.T) {
	mk := func(name, path string) familyView {
		return familyView{Name: name, Versions: []versionView{{Path: path}}}
	}
	vs := []familyView{mk("ТЗ", "/Уроки/ТЗ.docx"), mk("ТЗ", "/Работа/ТЗ.docx"), mk("Смета", "/a/Смета.xlsx")}
	disambiguate(vs)
	if vs[0].Name != "ТЗ (Уроки)" || vs[1].Name != "ТЗ (Работа)" {
		t.Errorf("одинаковые имена не различены папкой: %q, %q", vs[0].Name, vs[1].Name)
	}
	if vs[2].Name != "Смета" {
		t.Errorf("уникальное имя тронуто: %q", vs[2].Name)
	}
}

func TestSortViews_EqualDatesAndNamesGoByFirstPath(t *testing.T) {
	d := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	mk := func(p string) familyView {
		return familyView{Name: "ТЗ", latest: d, Versions: []versionView{{Path: p}}}
	}
	a := []familyView{mk("/б/ТЗ.docx"), mk("/а/ТЗ.docx")}
	sortViews(a)
	if a[0].Versions[0].Path != "/а/ТЗ.docx" {
		t.Errorf("при равных дате и имени порядок по пути: %v", a)
	}
}
