package localfiles

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxDocBytes — порог файла для docx, pdf и картинок: документ весит картинками и шрифтами, а
// не текстом, и порог текстовых файлов отсек бы почти каждое ТЗ. В память из
// него попадает не больше maxDocXMLBytes разметки и MaxFileBytes текста.
const maxDocBytes = 32 << 20

// maxDocXMLBytes — сколько разметки docx распаковывается: сжатый «zip-бомбой»
// документ не раздувает память. Переменная, а не константа, только ради теста
// обрыва разметки.
var maxDocXMLBytes int64 = 64 << 20

var errNoDocumentXML = errors.New("docx without word/document.xml")

// wordNS — пространства имен WordprocessingML (обычное и Strict). Текст берется
// только из них: a:t внутри w:drawing — подпись фигуры, а не абзац (ревью #378).
var wordNS = map[string]bool{
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main": true,
	"http://purl.oclc.org/ooxml/wordprocessingml/main":             true,
}

// isDocx tells whether a file is read through docxText rather than as text.
func isDocx(name string) bool { return strings.EqualFold(filepath.Ext(name), ".docx") }

// isOffice tells pptx and xlsx: их читает только обход версий (versions.go).
func isOffice(name string) bool {
	e := strings.ToLower(filepath.Ext(name))
	return e == ".pptx" || e == ".xlsx"
}

// maxBytes is the size limit for a file on disk.
func maxBytes(name string) int64 {
	if isDocx(name) || extractable(name) || isOffice(name) {
		return maxDocBytes
	}
	return MaxFileBytes
}

// readDocx extracts the text of a docx opened through the root.
//
// ponytail: сниппет каждого найденного docx читается заново — распаковка и
// разбор, миллисекунды на обычном ТЗ. Потолок: десятки docx по 30 МБ в одной
// выдаче. Путь: хранить строки сниппета в индексе.
func readDocx(root *os.Root, rel string) (string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // только чтение
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	text, err := docxText(f, info.Size())
	if err != nil {
		return "", fmt.Errorf("%w: %w", sniffNotDocx(f, info.Size()), err)
	}
	return text, nil
}

// docxText returns the paragraphs of word/document.xml, one per line.
func docxText(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("docx: %w", err)
	}
	for _, zf := range zr.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", fmt.Errorf("docx: %w", err)
		}
		defer func() { _ = rc.Close() }()
		lr := &io.LimitedReader{R: rc, N: maxDocXMLBytes}
		text, err := wordText(lr)
		if err != nil && lr.N <= 0 {
			// Предел распаковки оборвал разметку на полутеге: собранный текст
			// остается, иначе пропал бы весь документ.
			return text, nil
		}
		return text, err
	}
	return "", errNoDocumentXML
}

// wordText collects the text runs of WordprocessingML: w:t — текст, w:tab —
// табуляция, w:br и конец w:p — перевод строки. Остальная разметка (стили,
// поля, картинки) пропускается. При ошибке разбора возвращает и собранное.
func wordText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var w wordWriter
	for w.b.Len() < MaxFileBytes {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return capText(w.b.String()), fmt.Errorf("docx: %w", err)
		}
		w.token(tok)
	}
	return capText(w.b.String()), nil
}

// wordWriter turns WordprocessingML tokens into plain text.
type wordWriter struct {
	b      strings.Builder
	inText bool // внутри w:t: только там CharData — текст документа
}

func (w *wordWriter) token(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		if wordNS[t.Name.Space] {
			w.start(t.Name.Local)
		}
	case xml.EndElement:
		if !wordNS[t.Name.Space] {
			return
		}
		switch t.Name.Local {
		case "t":
			w.inText = false
		case "p":
			w.b.WriteByte('\n')
		}
	case xml.CharData:
		if w.inText {
			w.b.Write(t)
		}
	}
}

func (w *wordWriter) start(name string) {
	switch name {
	case "t":
		w.inText = true
	case "tab":
		w.b.WriteByte('\t')
	case "br", "cr":
		w.b.WriteByte('\n')
	}
}

// capText cuts s to MaxFileBytes on a rune boundary.
func capText(s string) string {
	if len(s) <= MaxFileBytes {
		return s
	}
	cut := MaxFileBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// DocxText returns the text of a .docx held elsewhere than a source folder —
// файл, приложенный к вопросу чата, или скачанный по ссылке.
func DocxText(r io.ReaderAt, size int64) (string, error) { return docxText(r, size) }
