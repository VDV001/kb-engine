package docxwrite_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/docxwrite"
)

func open(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("не zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

// paragraphs reads the text of w:p as Word would: каждый абзац отдельно.
func paragraphs(t *testing.T, doc string) []string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var out []string
	var cur strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("document.xml не разбирается: %v", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			inT = e.Name.Local == "t"
		case xml.EndElement:
			if e.Name.Local == "p" {
				out = append(out, cur.String())
				cur.Reset()
			}
			inT = false
		case xml.CharData:
			if inT {
				cur.Write(e)
			}
		}
	}
}

func TestBytes_ValidDocxWithAParagraphPerLine(t *testing.T) {
	b, err := docxwrite.Bytes("Цена 100\n\nСкидка <5%> & гарантия\n  отступ сохранен")
	if err != nil {
		t.Fatal(err)
	}
	files := open(t, b)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if _, ok := files[name]; !ok {
			t.Errorf("в docx нет %s: %v", name, files)
		}
	}
	got := paragraphs(t, files["word/document.xml"])
	want := []string{"Цена 100", "", "Скидка <5%> & гарантия", "  отступ сохранен"}
	if !slices.Equal(got, want) {
		t.Errorf("абзацы %q, ждали %q", got, want)
	}
}

func TestBytes_EmptyTextIsRefused(t *testing.T) {
	if _, err := docxwrite.Bytes("  \n "); err == nil {
		t.Error("пустой документ создан")
	}
}
