// Package docxwrite builds a minimal .docx from plain text: новая итерация
// документа, которую чат предложил сохранить (#548), должна быть документом, а
// не заметкой, чтобы встать в ленту версий рядом с остальными.
package docxwrite

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
)

const (
	contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`
	rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`
	docHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`
	docTail = `</w:body></w:document>`
)

// Bytes returns a .docx with one paragraph per line of text; пустая строка —
// пустой абзац, отступы сохраняются.
func Bytes(text string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("docx: нет текста")
	}
	var doc strings.Builder
	doc.WriteString(docHead)
	for line := range strings.SplitSeq(strings.ReplaceAll(strings.TrimRight(text, "\n"), "\r\n", "\n"), "\n") {
		var esc bytes.Buffer
		if err := xml.EscapeText(&esc, []byte(line)); err != nil {
			return nil, err
		}
		doc.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + esc.String() + `</w:t></w:r></w:p>`)
	}
	doc.WriteString(docTail)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes}, {"_rels/.rels", rels}, {"word/document.xml", doc.String()},
	} {
		w, err := zw.Create(part.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(part.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
