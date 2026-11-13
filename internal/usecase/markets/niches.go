// Package markets reads what a marketplace seller brings to the window (#580):
// выгрузки ниш из сервиса аналитики.
package markets

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/daniil/kb-engine/internal/domain"
)

// Ошибки чтения файла ниш.
var (
	ErrEmptyFile     = errors.New("в файле нет строк")
	ErrNoNameColumn  = errors.New("не нашел колонку с названием ниши")
	ErrDuplicatePick = errors.New("одна величина выбрана у двух колонок")
	ErrNoSuchColumn  = errors.New("такой колонки в файле нет")
	ErrNotANumber    = errors.New("не число")
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

// Table — прочитанный файл ниш. HeaderRow — номер строки заголовков с 1.
type Table struct {
	HeaderRow int
	Columns   []Column
	Niches    []*domain.Niche
	Problems  []Problem
}

// headerScan — сколько строк сверху смотреть в поисках заголовков: над ними
// у выгрузок бывает название отчета и пустые строки.
const headerScan = 10

// ReadNiches reads the rows of an export. picks — выбор человека: номер
// колонки → величина, "" — колонка не нужна; выбор главнее узнанного.
// Без колонки с названием возвращает колонки и ErrNoNameColumn — окно
// спрашивает, какая из них ниша.
func ReadNiches(rows [][]string, picks map[int]domain.NicheColumn) (Table, error) {
	h := headerRow(rows)
	if h < 0 {
		return Table{}, ErrEmptyFile
	}
	cols, err := columns(rows[h], picks)
	if err != nil {
		return Table{}, err
	}
	tb := Table{HeaderRow: h + 1, Columns: cols}
	nameAt := -1
	for _, c := range cols {
		if c.Field == domain.NicheName {
			nameAt = c.Index
		}
	}
	if nameAt < 0 {
		return tb, ErrNoNameColumn
	}

	type parsed struct {
		row    int
		name   string
		values map[domain.NicheColumn]float64
	}
	var read []parsed
	percent := map[domain.NicheColumn]bool{} // колонка долей записана процентами
	for i := h + 1; i < len(rows); i++ {
		name := strings.TrimSpace(cell(rows[i], nameAt))
		if name == "" || isTotal(name) {
			continue
		}
		p := parsed{row: i + 1, name: name, values: map[domain.NicheColumn]float64{}}
		bad := ""
		for _, c := range cols {
			if c.Field == "" || c.Field == domain.NicheName {
				continue
			}
			raw := cell(rows[i], c.Index)
			v, pct, empty, err := parseNumber(raw)
			if err != nil {
				bad = fmt.Sprintf("«%s»: %v «%s»", c.Header, ErrNotANumber, strings.TrimSpace(raw))
				break
			}
			if empty {
				continue
			}
			p.values[c.Field] = v
			if isShare(c.Field) && (pct || math.Abs(v) > 1) {
				percent[c.Field] = true
			}
		}
		if bad != "" {
			tb.Problems = append(tb.Problems, Problem{Row: p.row, Reason: bad})
			continue
		}
		read = append(read, p)
	}
	for _, p := range read {
		for f := range percent {
			if v, ok := p.values[f]; ok {
				p.values[f] = v / 100
			}
		}
		n, err := domain.NewNiche(p.name, p.values)
		if err != nil {
			tb.Problems = append(tb.Problems, Problem{Row: p.row, Reason: err.Error()})
			continue
		}
		tb.Niches = append(tb.Niches, n)
	}
	return tb, nil
}

// headerRow finds the header: первая строка сверху, где узнаны хотя бы две
// колонки, иначе первая непустая; -1 — строк нет.
func headerRow(rows [][]string) int {
	first := -1
	for i := 0; i < len(rows) && i < headerScan; i++ {
		known := 0
		for _, c := range rows[i] {
			if _, ok := domain.RecognizeNicheColumn(c); ok {
				known++
			}
		}
		if known >= 2 {
			return i
		}
		if first < 0 && !blank(rows[i]) {
			first = i
		}
	}
	if first < 0 {
		for i := headerScan; i < len(rows); i++ {
			if !blank(rows[i]) {
				return i
			}
		}
	}
	return first
}

// columns names each header cell: выбор человека, иначе узнанное; одна
// величина — одна колонка, узнанный повтор остается неузнанным.
func columns(header []string, picks map[int]domain.NicheColumn) ([]Column, error) {
	picked := map[domain.NicheColumn]int{}
	for i, f := range picks {
		if i < 0 || i >= len(header) {
			return nil, fmt.Errorf("%w: %d", ErrNoSuchColumn, i+1)
		}
		if f == "" {
			continue
		}
		if _, err := domain.ParseNicheColumn(string(f)); err != nil {
			return nil, err
		}
		if j, dup := picked[f]; dup {
			return nil, fmt.Errorf("%w: «%s» у колонок %d и %d", ErrDuplicatePick, f.Label(), min(i, j)+1, max(i, j)+1)
		}
		picked[f] = i
	}
	cols := make([]Column, len(header))
	taken := map[domain.NicheColumn]bool{}
	for f := range picked {
		taken[f] = true
	}
	for i, hd := range header {
		cols[i] = Column{Index: i, Header: strings.TrimSpace(hd)}
		if f, ok := picks[i]; ok {
			cols[i].Field = f
			continue
		}
		if f, ok := domain.RecognizeNicheColumn(hd); ok && !taken[f] {
			cols[i].Field = f
			taken[f] = true
		}
	}
	return cols, nil
}

func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

func blank(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// isTotal — строка итога внизу выгрузки, не ниша.
func isTotal(name string) bool {
	f := domain.FoldName(name)
	return strings.HasPrefix(f, "итого") || strings.HasPrefix(f, "всего")
}

func isShare(f domain.NicheColumn) bool { return f == domain.NicheGrowth || f == domain.NicheTop10 }

// parseNumber reads a cell: «182 000», «7 490,50 ₽», «12 %», «−6». empty —
// ячейка пустая или прочерк: величины нет, это не ошибка.
//
// ponytail: одна запятая — всегда дробная часть («1,234» — 1.234), как в
// русских выгрузках. Потолок — файл с английскими тысячами «1,234»; путь —
// выбор разделителя в окне, когда такой файл придет.
func parseNumber(raw string) (v float64, percent, empty bool, err error) {
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		if r == '−' || r == '–' {
			return '-'
		}
		return r
	}, strings.ToLower(raw))
	for _, unit := range []string{"₽", "руб.", "руб", "р.", "шт.", "шт", "$"} {
		s = strings.ReplaceAll(s, unit, "")
	}
	if strings.Contains(s, "%") {
		percent = true
		s = strings.ReplaceAll(s, "%", "")
	}
	if s == "" || s == "-" || s == "—" {
		return 0, false, true, nil
	}
	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0: // последний знак — дробный, другой — тысячи
		if dot > comma {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
		}
	case strings.Count(s, ",") > 1:
		s = strings.ReplaceAll(s, ",", "")
	case comma >= 0:
		s = strings.ReplaceAll(s, ",", ".")
	case strings.Count(s, ".") > 1:
		s = strings.ReplaceAll(s, ".", "")
	}
	v, err = strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false, false, ErrNotANumber
	}
	return v, percent, false, nil
}
