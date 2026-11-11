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

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
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
