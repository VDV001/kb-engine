package localfiles

import (
	"archive/zip"
	"errors"
	"io/fs"
)

// errNotUTF8 — текстовый файл не в UTF-8: другая кодировка (Windows-1251)
// или двоичный файл с текстовым расширением.
var errNotUTF8 = errors.New("not UTF-8 text")

// Explain says in a person's words why a file could not be read — окно DVK
// показывает это по кнопке «подробнее», и причина подсказывает, что сделать.
func Explain(err error) string {
	switch {
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
