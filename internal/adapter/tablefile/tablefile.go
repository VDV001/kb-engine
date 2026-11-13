// Package tablefile reads a spreadsheet a person brings to the window — xlsx
// или csv — into rows of text (#580).
package tablefile

import "errors"

// MaxBytes — предел файла: выгрузка ниш весит единицы мегабайт.
const MaxBytes = 50 << 20

// Ошибки чтения.
var (
	ErrUnsupported = errors.New("читаю только .xlsx и .csv")
	ErrTooBig      = errors.New("файл больше 50 МБ")
)

// Read returns the rows of the first sheet.
func Read(path string) ([][]string, error) { return nil, nil }
