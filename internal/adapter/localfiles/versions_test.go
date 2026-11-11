package localfiles_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// writePptx кладет pptx с текстом на слайдах: каждый элемент slides — абзацы одного слайда.
func writePptx(t *testing.T, root, rel string, slides ...[]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, paras := range slides {
		w, err := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i+1))
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, p := range paras {
			b.WriteString(`<a:p><a:r><a:t>` + p + `</a:t></a:r></a:p>`)
		}
		fmt.Fprintf(w, `<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" `+
			`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree>%s</p:spTree></p:cSld></p:sld>`, b.String())
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return write(t, root, rel, buf.String())
}

func writeXlsx(t *testing.T, root, rel string, cells map[string]any) string {
	t.Helper()
	f := excelize.NewFile()
	for cell, v := range cells {
		if err := f.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(root, rel)
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func collect(t *testing.T, s *localfiles.Source) map[string]versions.File {
	t.Helper()
	fs, err := s.Versions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]versions.File{}
	for _, f := range fs {
		out[f.Path] = f
	}
	return out
}

func newSource(t *testing.T, root string) *localfiles.Source {
	t.Helper()
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestVersions_ReadsDocxPptxXlsxOfAFamily(t *testing.T) {
	root := t.TempDir()
	d1 := writeDocx(t, root, "p/ТЗ_2026_11_05.docx", `<w:p><w:r><w:t>Цель</w:t></w:r></w:p>`, false)
	d2 := writeDocx(t, root, "p/ТЗ_2026_11_10.docx", `<w:p><w:r><w:t>Цель</w:t></w:r></w:p><w:p><w:r><w:t>Сроки</w:t></w:r></w:p>`, false)
	p1 := writePptx(t, root, "p/КП_2026_11_05.pptx", []string{"Заголовок"}, []string{"Цена 100"})
	p2 := writePptx(t, root, "p/КП_2026_11_10.pptx", []string{"Заголовок"}, []string{"Цена 120"})
	x1 := writeXlsx(t, root, "p/Смета v1.xlsx", map[string]any{"A1": "Работа", "B1": 100})
	x2 := writeXlsx(t, root, "p/Смета v2.xlsx", map[string]any{"A1": "Работа", "B1": 120})

	got := collect(t, newSource(t, root))
	for _, p := range []string{d1, d2, p1, p2, x1, x2} {
		f, ok := got[p]
		if !ok {
			t.Fatalf("%s не попал в обход: %v", p, got)
		}
		if len(f.Hash) != 64 || f.Text == "" {
			t.Errorf("%s: hash %q, text %q", p, f.Hash, f.Text)
		}
	}
	if got[d2].Text != "Цель\nСроки\n" {
		t.Errorf("docx: %q", got[d2].Text)
	}
	if tx := got[p2].Text; !strings.Contains(tx, "Слайд 2") || !strings.Contains(tx, "Цена 120") || strings.Contains(tx, "Цена 100") {
		t.Errorf("pptx: %q", tx)
	}
	if tx := got[x2].Text; !strings.Contains(tx, "Работа") || !strings.Contains(tx, "120") {
		t.Errorf("xlsx: %q", tx)
	}
	// вся цепочка до подписей: правка цены видна как «убрано/добавлено»
	fams := versions.Group([]versions.File{got[p1], got[p2]})
	if n := fams[0].Versions[1].Note; !strings.Contains(n, "Цена 120") || !strings.Contains(n, "убрано 1") {
		t.Errorf("подпись: %q", n)
	}
}

func TestVersions_CopyInAnotherFolderHasSameHash(t *testing.T) {
	root := t.TempDir()
	a := writePptx(t, root, "p/КП.pptx", []string{"Цена"})
	c := write(t, root, "Загрузки/КП (1).pptx", mustRead(t, a))
	got := collect(t, newSource(t, root))
	if got[a].Hash == "" || got[a].Hash != got[c].Hash {
		t.Errorf("копия: %q и %q", got[a].Hash, got[c].Hash)
	}
}

func TestVersions_LoneFileIsNotHashedOrRead(t *testing.T) {
	root := t.TempDir()
	one := writePptx(t, root, "p/Одинокий.pptx", []string{"Текст"})
	f, ok := collect(t, newSource(t, root))[one]
	if !ok {
		t.Fatal("файл не виден")
	}
	if f.Hash != "" || f.Text != "" {
		t.Errorf("семейство из одного файла не читается: hash %q, text %q", f.Hash, f.Text)
	}
}

func TestVersions_SkipsSecretsJunkAndOtherTypes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/code_v1.go", "package a")
	write(t, root, "node_modules/x/Отчет_v1.docx", "x")
	write(t, root, "a/credentials_v1.xlsx", "x")
	write(t, root, "a/Отчет.bak.docx", "x")
	ok := writePptx(t, root, "a/Деки_v1.pptx", []string{"a"})
	got := collect(t, newSource(t, root))
	if len(got) != 1 || got[ok].Path == "" {
		t.Errorf("ждали только %s: %v", ok, got)
	}
}

func TestVersions_BrokenFileStaysWithoutText(t *testing.T) {
	root := t.TempDir()
	a := write(t, root, "p/Дек_2026_11_05.pptx", "это не zip")
	b := write(t, root, "p/Дек_2026_11_10.pptx", "и это не zip")
	got := collect(t, newSource(t, root))
	if got[a].Text != "" || got[b].Text != "" || got[a].Hash == "" {
		t.Errorf("битый файл: %+v %+v", got[a], got[b])
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
