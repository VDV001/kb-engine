package localfiles

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxDocBytes — порог для docx и pdf. Они весят картинками и шрифтами, а не
// текстом, поэтому порог текстовых файлов отсек бы почти каждый скан ТЗ; текст
// из них все равно режется по MaxFileBytes.
const MaxDocBytes = 32 << 20

const (
	// maxDocXMLBytes — сколько разметки docx читается: распаковка ограничена,
	// чтобы сжатый «zip-бомбой» документ не раздул память.
	maxDocXMLBytes = 64 << 20
	// pdfTimeout — дольше pdftotext на одном файле не работает: битый или
	// огромный PDF не останавливает сборку индекса.
	pdfTimeout = 20 * time.Second
)

var errNoDocumentXML = errors.New("docx without word/document.xml")

// pdftotextDirs — где искать pdftotext, если его нет в PATH: окно, открытое из
// Finder, получает урезанный PATH без nix и Homebrew.
var pdftotextDirs = []string{"/run/current-system/sw/bin", "/opt/homebrew/bin", "/usr/local/bin"}

func findPdftotext() string {
	if p, err := exec.LookPath("pdftotext"); err == nil {
		return p
	}
	for _, d := range pdftotextDirs {
		p := filepath.Join(d, "pdftotext")
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// ReadsPDF reports whether PDFs are indexed: pdftotext was found when the
// source was opened.
func (s *Source) ReadsPDF() bool { return s.pdftotext != "" }

// docExt tells whether a file name is a document read by an extractor.
func docExt(name string) string {
	switch ext := strings.ToLower(filepath.Ext(name)); ext {
	case ".docx", ".pdf":
		return ext
	}
	return ""
}

// maxBytes is the size limit for a file on disk.
func maxBytes(name string) int64 {
	if docExt(name) != "" {
		return MaxDocBytes
	}
	return MaxFileBytes
}

// readDoc extracts the text of a docx or pdf opened through the root.
func (s *Source) readDoc(root *os.Root, rel, ext string) (string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // только чтение
	if ext == ".pdf" {
		return pdfText(s.pdftotext, f)
	}
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	return docxText(f, info.Size())
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
		return wordText(io.LimitReader(rc, maxDocXMLBytes))
	}
	return "", errNoDocumentXML
}

// wordText collects the text runs of WordprocessingML: w:t — текст, w:tab —
// табуляция, w:br и конец w:p — перевод строки. Остальная разметка (стили,
// поля, картинки) пропускается.
func wordText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var w wordWriter
	for w.b.Len() < MaxFileBytes {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("docx: %w", err)
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
		w.start(t.Name.Local)
	case xml.EndElement:
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

// pdfText runs pdftotext over r. Вывод читается не больше MaxFileBytes: дальше
// процесс останавливается, а не дописывает в никуда.
func pdfText(bin string, r io.Reader) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pdfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-q", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = r
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	b, rerr := io.ReadAll(io.LimitReader(out, MaxFileBytes))
	truncated := len(b) == MaxFileBytes
	if truncated {
		cancel() // остальной текст не нужен
	}
	werr := cmd.Wait()
	if rerr != nil {
		return "", fmt.Errorf("pdftotext: %w", rerr)
	}
	if werr != nil && !truncated {
		return "", fmt.Errorf("pdftotext: %w", werr)
	}
	return capText(strings.ToValidUTF8(string(b), "")), nil
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
