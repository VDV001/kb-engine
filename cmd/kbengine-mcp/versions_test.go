package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/versionbook"
)

func docx(t *testing.T, path string, paras ...string) {
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

// Окно записало конфиг и файл заметок рядом: MCP отдает те же версии с теми
// же заметками и значком, текст — только версий из списка.
func TestVersionsAgent_ServesWhatTheWindowShows(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "docs")
	v1, v2 := filepath.Join(root, "КП_2026_11_05.docx"), filepath.Join(root, "КП_2026_11_10.docx")
	docx(t, v1, "Цена 100", "Срок 7 дней")
	docx(t, v2, "Цена 100", "Срок 5 дней", "Гарантия")
	cfgPath := filepath.Join(dir, "kb-desktop", "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	x, err := newFileIndex(cfgPath, dir, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	// как у окна: заметки и значок лежат рядом с config.json
	window := versionbook.New(x.versionsSource, filepath.Join(filepath.Dir(cfgPath), "version-marks.json"))
	if _, err := window.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	if err := window.Mark(v1, "отправлено Алексею"); err != nil {
		t.Fatal(err)
	}
	if err := window.SetCurrent(v2, true); err != nil {
		t.Fatal(err)
	}

	a := newVersionsAgent(x)
	fams, err := a.Families(context.Background(), "кп")
	if err != nil || len(fams) != 1 || len(fams[0].Versions) != 2 {
		t.Fatalf("семейства агента: %+v %v", fams, err)
	}
	first, second := fams[0].Versions[0], fams[0].Versions[1]
	if first.Mark != "отправлено Алексею" || second.Mark != "" || !second.Current || first.Date != "05.11.2026" || !strings.Contains(second.Note, "Гарантия") {
		t.Errorf("ленты окна и агента разошлись: %+v", fams[0].Versions)
	}
	if text, err := a.Text(context.Background(), v1); err != nil || !strings.Contains(text, "Срок 7 дней") {
		t.Errorf("текст версии: %q %v", text, err)
	}
	if text, err := a.Text(context.Background(), "/etc/hosts"); err == nil {
		t.Errorf("агент получил текст файла вне списка: %q", text)
	}
	raw, _ := json.Marshal(fams)
	if strings.Contains(string(raw), dir+"/kb-desktop") {
		t.Errorf("в ответе путь к данным окна: %s", raw)
	}

	// окно сменило папки: агент идет за конфигом без перезапуска
	other := filepath.Join(dir, "other")
	docx(t, filepath.Join(other, "Смета_2026_11_01.docx"), "а")
	docx(t, filepath.Join(other, "Смета_2026_11_09.docx"), "а", "б")
	later := time.Now().Add(2 * time.Second)
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{other}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cfgPath, later, later); err != nil {
		t.Fatal(err)
	}
	fams, err = a.Families(context.Background(), "смет")
	if err != nil || len(fams) != 1 {
		t.Errorf("после смены папок: %+v %v", fams, err)
	}
}
