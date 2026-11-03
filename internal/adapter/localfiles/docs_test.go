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

// minimalPDF собирает одностраничный PDF с ASCII-строкой text: заголовок,
// четыре объекта и таблица xref с настоящими смещениями.
func minimalPDF(text string) string {
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.String()
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

// PDF читается через pdftotext. Без него на машине PDF не индексируется и
// не читается — как прежде, а не падает.
func TestReadText_Pdf(t *testing.T) {
	root := t.TempDir()
	path := write(t, root, "docs/report.pdf", minimalPDF("unicorn quarterly report"))
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.ReadsPDF() {
		if got := walked(t, s); len(got) != 0 {
			t.Fatalf("без pdftotext pdf в обходе: %q", got)
		}
		t.Skip("pdftotext нет на машине: проверено только, что pdf не индексируется")
	}
	if got := walked(t, s); !slices.Equal(got, []string{path}) {
		t.Fatalf("walked = %q", got)
	}
	got, err := s.ReadText(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "unicorn quarterly report") {
		t.Fatalf("ReadText = %q", got)
	}
}

// RTF по-прежнему не индексируется: извлекателя для него нет.
func TestWalk_StillSkipsRtf(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.rtf", `{\rtf1 hello}`)
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); len(got) != 0 {
		t.Fatalf("walked = %q", got)
	}
	if _, err := s.ReadText(p); !errors.Is(err, localfiles.ErrNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}
