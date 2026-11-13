// Package markets reads what a marketplace seller brings to the window (#580):
// выгрузки ниш из сервиса аналитики.
package markets

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
)

// Ошибки чтения файла ниш.
var (
	ErrEmptyFile     = errors.New("в файле нет строк")
	ErrNoNameColumn  = errors.New("не нашел колонку с названием ниши")
	ErrDuplicatePick = errors.New("одна величина выбрана у двух колонок")
	ErrNoSuchColumn  = errors.New("такой колонки в файле нет")
)

// Column — колонка файла и что в ней; Field "" — не узнана или не нужна.
type Column struct {
	Index  int
	Header string
	Field  domain.NicheColumn
}

// Problem — строка, которую не прочитали, и почему; Row — номер строки файла с 1.
type Problem struct {
	Row    int
	Reason string
}

// Table — прочитанный файл ниш.
type Table struct {
	HeaderRow int
	Columns   []Column
	Niches    []*domain.Niche
	Problems  []Problem
}

// ReadNiches reads the rows of an export.
func ReadNiches(rows [][]string, picks map[int]domain.NicheColumn) (Table, error) {
	return Table{}, nil
}
