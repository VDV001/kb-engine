// Package tablefile reads a spreadsheet a person brings to the window — xlsx
// или csv — into rows of text (#580).
package tablefile

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

// MaxBytes — предел файла: выгрузка ниш весит единицы мегабайт.
const MaxBytes = 50 << 20

// Ошибки чтения.
var (
	ErrUnsupported = errors.New("читаю только .xlsx и .csv")
	ErrTooBig      = errors.New("файл больше 50 МБ")
)

// Read returns the rows of the first sheet (xlsx) or of the file (csv).
func Read(path string) ([][]string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".xlsx" && ext != ".csv" {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Base(path))
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxBytes {
		return nil, ErrTooBig
	}
	if ext == ".csv" {
		return readCSV(path)
	}
	return readXLSX(path)
}

// readXLSX reads the first sheet; числа — как записаны в ячейке, без формата
// отображения: «0.12», а не «12%».
func readXLSX(path string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil
	}
	return f.GetRows(sheets[0], excelize.Options{RawCellValue: true})
}

// readCSV reads a csv in UTF-8 (BOM is dropped) or Windows-1251, as Russian
// services and Excel save it.
func readCSV(path string) ([][]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) {
		if b, err = charmap.Windows1251.NewDecoder().Bytes(b); err != nil {
			return nil, err
		}
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.Comma = delimiter(b)
	r.FieldsPerRecord = -1 // у строк итога и заголовка отчета колонок меньше
	r.LazyQuotes = true
	return r.ReadAll()
}

// sniffLines — сколько строк сверху смотреть, решая разделитель: над таблицей
// бывает название отчета без разделителей (живая проверка #580).
const sniffLines = 10

// delimiter picks the separator by the first lines: табуляция, точка с
// запятой, иначе запятая. Запятая в тексте не решает: она есть и в названиях.
func delimiter(b []byte) rune {
	var tab, semi bool
	for i, line := range bytes.SplitN(b, []byte("\n"), sniffLines+1) {
		if i == sniffLines {
			break
		}
		tab = tab || bytes.ContainsRune(line, '\t')
		semi = semi || bytes.ContainsRune(line, ';')
	}
	switch {
	case tab:
		return '\t'
	case semi:
		return ';'
	}
	return ','
}

// sniffBytes — сколько байт сверху пробовать на кодировку и разделитель.
const sniffBytes = 64 << 10

// Rows streams a csv row by row without the MaxBytes limit: выгрузка MPStats
// «Товары категории» весит 650-800 МБ (живая проверка #580). Кодировку и
// разделитель решает по началу файла, как Read; xlsx не потоковый — ошибка.
func Rows(path string) iter.Seq2[[]string, error] {
	return func(yield func([]string, error) bool) {
		if ext := strings.ToLower(filepath.Ext(path)); ext != ".csv" {
			yield(nil, fmt.Errorf("%w потоком: %s", ErrUnsupported, filepath.Base(path)))
			return
		}
		f, err := os.Open(path)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = f.Close() }()
		br := bufio.NewReaderSize(f, sniffBytes)
		head, err := br.Peek(sniffBytes)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
			yield(nil, err)
			return
		}
		if bom := []byte("\xef\xbb\xbf"); bytes.HasPrefix(head, bom) {
			_, _ = br.Discard(len(bom)) // в буфере: Discard не читает файл и не падает
			head = head[len(bom):]
		}
		var src io.Reader = br
		if !utf8Head(head) {
			src = charmap.Windows1251.NewDecoder().Reader(br)
		}
		r := csv.NewReader(src)
		r.Comma = delimiter(head)
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		for {
			row, err := r.Read()
			if errors.Is(err, io.EOF) {
				return
			}
			if !yield(row, err) || err != nil {
				return
			}
		}
	}
}

// utf8Head reports whether the start of a file is UTF-8: последняя буква
// пробы может быть разрезана границей, она не делает файл Windows-1251.
func utf8Head(b []byte) bool {
	for cut := 0; cut < utf8.UTFMax && len(b) > cut; cut++ {
		if utf8.Valid(b[:len(b)-cut]) {
			return true
		}
	}
	return utf8.Valid(b)
}
