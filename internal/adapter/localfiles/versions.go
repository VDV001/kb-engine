package localfiles

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// drawingNS — пространства имен DrawingML (обычное и Strict): текст слайда
// лежит в a:t.
var drawingNS = map[string]bool{
	"http://schemas.openxmlformats.org/drawingml/2006/main": true,
	"http://purl.oclc.org/ooxml/drawingml/main":             true,
}

var slideName = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

// versionable — документы, у которых бывают итерации. Секреты и копии .bak
// не берутся, как и в поиске по тексту; слова «credential» и «secret» в имени
// исключают файл при любом расширении: таблица с доступами бывает xlsx.
func versionable(name string) bool {
	n := strings.ToLower(name)
	if isSecret(name) || strings.Contains(n, ".bak") || slices.ContainsFunc(secretWords, func(w string) bool { return strings.Contains(n, w) }) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx", ".pptx", ".xlsx", ".pdf":
		return true
	}
	return false
}

// Versions returns the documents under the roots (docx, pptx, xlsx, pdf).
// Хеш и текст считаются только у файлов, у которых в обходе есть однофамилец
// (versions.Key): одинокий документ не читается, обход по Загрузкам не
// превращается в хеширование всего диска. Файл, который не читается, остается
// без текста, но не пропадает. Файлы пользователя не меняются.
//
// ponytail: хеш и текст считаются заново при каждом вызове. Потолок: сотни
// документов по 30 МБ в семействах. Путь: кэш по размеру и времени изменения,
// как у extractcache.
func (s *Source) Versions(ctx context.Context) ([]versions.File, error) {
	var files []versions.File
	err := s.walkBy(ctx, versionable, func(shown string, info os.FileInfo, _ int) error {
		files = append(files, versions.File{Path: shown, Size: info.Size(), Mod: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	family := map[string]int{}
	for _, f := range files {
		family[versions.Key(f.Path)]++
	}
	for i := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if family[versions.Key(files[i].Path)] > 1 {
			s.fill(&files[i])
		}
	}
	return files, nil
}

// fill sets the hash and the text of a file; what cannot be read stays empty.
func (s *Source) fill(f *versions.File) {
	i, rel, ok := s.locate(f.Path)
	if !ok {
		return
	}
	f.Hash = fileHash(s.dirs[i], rel)
	switch {
	case extractable(rel):
		if s.extracted != nil && s.extractReady(i, rel) {
			f.Text, _ = s.readExtracted(i, rel, f.Path)
		}
	default:
		// Текст, прочитанный с ошибкой, не берется: половина слайда в сравнении
		// выглядела бы как «убрано N строк».
		if text, err := officeText(s.dirs[i], rel); err == nil {
			f.Text = text
		}
	}
}

func fileHash(root *os.Root, rel string) string {
	f, err := root.Open(rel)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // только чтение
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// officeText reads docx, pptx or xlsx opened through the root.
func officeText(root *os.Root, rel string) (string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // только чтение
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".docx":
		return docxText(f, info.Size())
	case ".pptx":
		return pptxText(f, info.Size())
	case ".xlsx":
		return xlsxText(f)
	}
	return "", fmt.Errorf("%w: %q", ErrNotAllowed, rel)
}

// pptxText returns the text of ppt/slides/slideN.xml: строка «Слайд N», затем
// абзацы слайда. Номер слайда в тексте нужен, чтобы добавленный слайд был
// виден как добавленная строка.
func pptxText(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("pptx: %w", err)
	}
	type slide struct {
		n int
		f *zip.File
	}
	var slides []slide
	for _, zf := range zr.File {
		if m := slideName.FindStringSubmatch(zf.Name); m != nil {
			n, _ := strconv.Atoi(m[1])
			slides = append(slides, slide{n, zf})
		}
	}
	if len(slides) == 0 {
		return "", errors.New("pptx without slides")
	}
	slices.SortFunc(slides, func(a, b slide) int { return a.n - b.n })
	var b strings.Builder
	for _, sl := range slides {
		if b.Len() >= MaxFileBytes {
			break
		}
		fmt.Fprintf(&b, "Слайд %d\n", sl.n)
		rc, err := sl.f.Open()
		if err != nil {
			return capText(b.String()), fmt.Errorf("pptx: %w", err)
		}
		err = drawingText(&io.LimitedReader{R: rc, N: maxDocXMLBytes}, &b)
		_ = rc.Close() // только чтение
		if err != nil {
			return capText(b.String()), err
		}
	}
	return capText(b.String()), nil
}

// drawingText writes the a:t runs of DrawingML, a paragraph per line.
func drawingText(r io.Reader, b *strings.Builder) error {
	dec := xml.NewDecoder(r)
	inText := false
	for b.Len() < MaxFileBytes {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("pptx: %w", err)
		}
		drawingToken(tok, &inText, b)
	}
	return nil
}

// drawingToken applies one token: текст берется только внутри a:t, конец a:p —
// перевод строки.
func drawingToken(tok xml.Token, inText *bool, b *strings.Builder) {
	switch t := tok.(type) {
	case xml.StartElement:
		if !drawingNS[t.Name.Space] {
			return
		}
		switch t.Name.Local {
		case "t":
			*inText = true
		case "br":
			b.WriteByte('\n')
		}
	case xml.EndElement:
		if !drawingNS[t.Name.Space] {
			return
		}
		switch t.Name.Local {
		case "t":
			*inText = false
		case "p":
			b.WriteByte('\n')
		}
	case xml.CharData:
		if *inText {
			b.Write(t)
		}
	}
}

// xlsxText returns the sheets of a workbook: «Лист: имя», затем непустые
// строки, ячейки через « | ».
func xlsxText(r io.Reader) (string, error) {
	wb, err := excelize.OpenReader(r)
	if err != nil {
		return "", fmt.Errorf("xlsx: %w", err)
	}
	defer func() { _ = wb.Close() }() // только чтение
	var b strings.Builder
	for _, name := range wb.GetSheetList() {
		if b.Len() >= MaxFileBytes {
			break
		}
		fmt.Fprintf(&b, "Лист: %s\n", name)
		rows, err := wb.GetRows(name)
		if err != nil {
			return capText(b.String()), fmt.Errorf("xlsx: %w", err)
		}
		for _, row := range rows {
			if line := strings.TrimRight(strings.Join(row, " | "), " |"); line != "" {
				b.WriteString(line + "\n")
			}
		}
	}
	return capText(b.String()), nil
}
