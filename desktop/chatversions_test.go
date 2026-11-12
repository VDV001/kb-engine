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
	return newVersionsDesk(func() *localfiles.Source { return src }, filepath.Join(t.TempDir(), "version-marks.json"))
}

// kp кладет две версии КП и возвращает окно, пути версий и собранный список.
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

func TestChatVersions_SameFamiliesNotesAndBadgesAsTheTab(t *testing.T) {
	_, d, v1, v2 := kp(t)
	if err := d.Mark(v1, "отправлено Алексею"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetCurrent(v2, true); err != nil {
		t.Fatal(err)
	}
	fams, err := chatVersions{d}.Families(context.Background(), "кп")
	if err != nil || len(fams) != 1 || len(fams[0].Versions) != 2 {
		t.Fatalf("семейства для чата: %+v %v", fams, err)
	}
	a, b := fams[0].Versions[0], fams[0].Versions[1]
	if a.Path != v1 || a.Date != "05.11.2026" || a.Mark != "отправлено Алексею" || a.Current || a.Note != "первая версия" {
		t.Errorf("старая версия: %+v", a)
	}
	if b.Path != v2 || !b.Current || !strings.Contains(b.Note, "Гарантия") {
		t.Errorf("новая версия: %+v", b)
	}
}

func TestChatVersions_TextOnlyOfVersionsTheTabShows(t *testing.T) {
	root, d, v1, _ := kp(t)
	got, err := chatVersions{d}.Text(context.Background(), v1)
	if err != nil || !strings.Contains(got, "Срок 7 дней") {
		t.Errorf("текст версии: %q %v", got, err)
	}
	other := filepath.Join(root, "p", "Чужой.docx")
	docxFile(t, other, "секрет")
	for name, p := range map[string]string{"не из списка": other, "вне папок": "/etc/hosts"} {
		if text, err := (chatVersions{d}).Text(context.Background(), p); err == nil {
			t.Errorf("%s: модель получила текст %q", name, text)
		}
	}
}

func TestChatVersions_NilWithoutTheDesk(t *testing.T) {
	if (&chatDesk{}).versionsAccess() != nil {
		t.Error("без вкладки «Артефакты» инструментов версий быть не должно")
	}
	if (&chatDesk{versions: versionsDeskOver(t, t.TempDir())}).versionsAccess() == nil {
		t.Error("с вкладкой инструменты версий должны быть")
	}
}
