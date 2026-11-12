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
	return newVersionsDesk(func() *localfiles.Source { return src })
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
	d := newVersionsDesk(func() *localfiles.Source { return nil })
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
		{"много и длинно", versions.Change{Kind: versions.KindDiff, Added: []string{long, "b", "c", "d", "e"}}, "добавлено строк: 5 — «" + strings.Repeat("я", 60) + "…», «b», «c», …и ещё 2"},
	} {
		if got := noteText(c.in); got != c.want {
			t.Errorf("%s: %q, ждали %q", c.name, got, c.want)
		}
	}
}
