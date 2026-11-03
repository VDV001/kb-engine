package localfiles_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
)

// writeDocx кладет docx с одним word/document.xml; body — содержимое w:body.
// store=true пишет без сжатия: так размер файла на диске равен размеру текста.
func writeDocx(t *testing.T, root, rel, body string, store bool) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	method := zip.Deflate
	if store {
		method = zip.Store
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: method})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>%s</w:body></w:document>`, body)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return write(t, root, rel, buf.String())
}

// Документы Word ищутся по тексту: абзацы — строки, табуляция сохраняется.
// Прежде docx пропускались целиком, а это главные документы тендеров и КП.
func TestReadText_Docx(t *testing.T) {
	root := t.TempDir()
	path := writeDocx(t, root, "tenders/ТЗ.docx",
		`<w:p><w:r><w:t>Техническое</w:t></w:r><w:r><w:t xml:space="preserve"> задание</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>поставка</w:t><w:tab/><w:t>&lt;ПАК&gt;</w:t></w:r></w:p>`, false)
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); !slices.Equal(got, []string{path}) {
		t.Fatalf("walked = %q, ждали docx", got)
	}
	got, err := s.ReadText(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Техническое задание\nпоставка\t<ПАК>\n"; got != want {
		t.Fatalf("ReadText = %q, want %q", got, want)
	}
}

// Битый docx — ошибка чтения (сборка посчитает его в Skipped), а не отказ
// доступа и не паника.
func TestReadText_BrokenDocx(t *testing.T) {
	root := t.TempDir()
	tests := []struct{ name, body string }{
		{"не zip", "просто текст"},
		{"zip без document.xml", func() string {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			_, _ = zw.Create("other.xml")
			_ = zw.Close()
			return buf.String()
		}()},
	}
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := write(t, root, tt.name+".docx", tt.body)
			if _, err := s.ReadText(p); err == nil || errors.Is(err, localfiles.ErrNotAllowed) {
				t.Fatalf("err = %v, ждали ошибку чтения", err)
			}
		})
	}
}

// Документ крупнее порога текстовых файлов индексируется: docx и pdf весят
// картинками, а не текстом. Текст из него режется по тому же порогу, что и
// текстовый файл, — и сжатый «zip-бомбой» docx не раздувает память.
func TestReadText_DocsSizeLimits(t *testing.T) {
	root := t.TempDir()
	padded := writeDocx(t, root, "big.docx",
		`<w:p><w:r><w:t>единорог</w:t></w:r></w:p><!--`+strings.Repeat("x", localfiles.MaxFileBytes+1)+`-->`, true)
	bomb := writeDocx(t, root, "bomb.docx",
		`<w:p><w:r><w:t>`+strings.Repeat("ё", localfiles.MaxFileBytes)+`</w:t></w:r></w:p>`, false)
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); !slices.Equal(got, []string{padded, bomb}) {
		t.Fatalf("walked = %q, ждали оба docx", got)
	}
	if got, err := s.ReadText(padded); err != nil || got != "единорог\n" {
		t.Fatalf("большой docx: %q, %v", got, err)
	}
	got, err := s.ReadText(bomb)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > localfiles.MaxFileBytes || !utf8.ValidString(got) {
		t.Fatalf("текст из docx: %d байт, valid=%v; ждали не больше %d и целые руны", len(got), utf8.ValidString(got), localfiles.MaxFileBytes)
	}
}

// PDF и RTF не индексируются и не читаются: извлекателя для них в чистом Go
// нет, а внешний pdftotext из poppler отвергнут — лишняя зависимость и разбор
// чужих PDF без песочницы (ревью #378). PDF прочитает помощник на PDFKit.
func TestWalk_SkipsUnreadDocs(t *testing.T) {
	tests := []struct{ name, rel, body string }{
		{"rtf", "a.rtf", `{\rtf1 hello}`},
		{"pdf", "docs/report.pdf", "%PDF-1.4\n"},
		{"pdf в верхнем регистре", "SCAN.PDF", "%PDF-1.4\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			p := write(t, root, tt.rel, tt.body)
			s, err := localfiles.New([]string{root}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := walked(t, s); len(got) != 0 {
				t.Fatalf("walked = %q", got)
			}
			if _, err := s.ReadText(p); !errors.Is(err, localfiles.ErrNotAllowed) {
				t.Fatalf("err = %v, ждали ErrNotAllowed", err)
			}
		})
	}
}

// Подписи фигур и диаграмм (DrawingML a:t внутри w:drawing) — не текст
// абзаца: они рвали абзац переводом строки, и сниппет показывал обрывок.
func TestReadText_DocxIgnoresDrawingText(t *testing.T) {
	root := t.TempDir()
	path := writeDocx(t, root, "a.docx",
		`<w:p><w:r><w:t>Подпись</w:t></w:r><w:r><w:drawing>`+
			`<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:p><a:r><a:t>фигура</a:t></a:r></a:p></a:graphic>`+
			`</w:drawing></w:r><w:r><w:t xml:space="preserve"> договора</w:t></w:r></w:p>`, false)
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadText(path); err != nil || got != "Подпись договора\n" {
		t.Fatalf("ReadText = %q, %v", got, err)
	}
}

// Разметка сверх предела распаковки обрывает XML на полутеге. Собранный до
// обрыва текст остается в индексе, а не пропадает вместе с документом.
func TestReadText_DocxCutXMLKeepsText(t *testing.T) {
	defer localfiles.SetMaxDocXML(400)()
	root := t.TempDir()
	path := writeDocx(t, root, "a.docx",
		`<w:p><w:r><w:t>единорог</w:t></w:r></w:p><w:p><w:r><w:t>`+strings.Repeat("x", 1000)+`</w:t></w:r></w:p>`, false)
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadText(path)
	if err != nil {
		t.Fatalf("обрезанная разметка: %v", err)
	}
	if !strings.HasPrefix(got, "единорог\n") {
		t.Fatalf("ReadText = %q, ждали первый абзац", got)
	}
}
