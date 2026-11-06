package localfiles

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

// errNotUTF8 — текстовый файл не в UTF-8: другая кодировка (Windows-1251)
// или двоичный файл с текстовым расширением.
var errNotUTF8 = errors.New("not UTF-8 text")

// Чем оказался файл с расширением .docx, который не открылся как документ
// Word, — по его содержимому (шесть «.docx» владельца были одной HTML-страницей:
// скачалась страница вместо файла).
var (
	errEmptyFile = errors.New("empty file")
	errWebPage   = errors.New("web page, not a Word document")
	errOldDoc    = errors.New("old Word format (.doc)")
	errPDFInside = errors.New("PDF, not a Word document")
	errNotDocx   = errors.New("not a Word document")
)

// htmlTitle finds the page title: по нему видно, что скачалось вместо файла.
var htmlTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// sniffNotDocx looks at the start of a file that did not open as .docx.
func sniffNotDocx(r io.ReaderAt, size int64) error {
	if size == 0 {
		return errEmptyFile
	}
	head := make([]byte, 8192) // заголовок страницы обычно в первых килобайтах
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}):
		return errOldDoc
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return errPDFInside
	case strings.HasPrefix(http.DetectContentType(head), "text/html"):
		if m := htmlTitle.FindSubmatch(head); m != nil {
			if t := strings.Join(strings.Fields(string(m[1])), " "); t != "" {
				return fmt.Errorf("%w: страница «%s»", errWebPage, t)
			}
		}
		return errWebPage
	default:
		return errNotDocx
	}
}

// Explain says in a person's words why a file could not be read — окно DVK
// показывает это по кнопке «подробнее», и причина подсказывает, что сделать.
func Explain(err error) string {
	switch {
	case errors.Is(err, errWebPage):
		return "это веб-страница, а не документ Word: скорее всего, при скачивании сохранилась страница сайта вместо файла — скачайте заново"
	case errors.Is(err, errOldDoc):
		return "старый формат Word (.doc) под именем .docx — откройте в Word и сохраните как .docx"
	case errors.Is(err, errPDFInside):
		return "это PDF под именем .docx — переименуйте в .pdf"
	case errors.Is(err, errEmptyFile):
		return "пустой файл — скачайте или сохраните заново"
	case errors.Is(err, errNotUTF8):
		return "другая кодировка, не UTF-8 (например, Windows-1251), или это не текст — пересохраните в UTF-8"
	case errors.Is(err, errNoDocumentXML), errors.Is(err, zip.ErrFormat), errors.Is(err, zip.ErrAlgorithm), errors.Is(err, zip.ErrChecksum):
		return "документ Word поврежден или это не .docx — откройте и пересохраните"
	case errors.Is(err, fs.ErrPermission):
		return "нет прав на чтение — дайте окну доступ к папке в настройках macOS"
	case errors.Is(err, ErrNotAllowed):
		return "файл вне правил поиска (служебная папка, секрет или еще не распознан)"
	default:
		return "не читается: " + err.Error()
	}
}
