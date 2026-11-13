package markets

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoDaily is returned when a file has no sales by day.
var ErrNoDaily = errors.New("в файле нет продаж по дням")

// DailyFormat — как в файле лежат продажи по дням.
type DailyFormat string

// Виды файла.
const (
	DailyWide DailyFormat = "wide" // строки — товары, колонки — дни
	DailyLong DailyFormat = "long" // строка — день (и товар), колонка штук
)

// DailyInfo — что прочитано: вид, товаров (у широкого), дней, период.
type DailyInfo struct {
	Format   DailyFormat
	Products int
	Days     int
	From, To time.Time
}

// minDayColumns — сколько колонок-дат делают файл широким: две могут быть
// «Дата создания» и «Дата обновления», три — уже ряд по дням.
const minDayColumns = 3

var dayLayouts = []string{"02.01.2006", "2006-01-02", "02.01.06", "2006.01.02", "02/01/2006"}

// excelEpoch — нулевой день чисел-дат Excel (с его ошибкой 1900 года).
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// parseDay reads a date cell: «01.11.2025», «2025-11-01», «01.09.26», с
// временем после пробела, или число Excel (xlsx отдает дату числом).
func parseDay(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if f := strings.Fields(s); len(f) > 1 {
		s = f[0]
	}
	for _, l := range dayLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	// Числа Excel 20000-80000 — 1954-2119 годы; меньше — это штуки, а не дата.
	if n, err := strconv.Atoi(s); err == nil && n >= 20000 && n <= 80000 {
		return excelEpoch.AddDate(0, 0, n), true
	}
	return time.Time{}, false
}

// ReadDaily reads sales by day, summed over the niche's products: широкий
// файл (дни колонками) или длинный (колонки «Дата» и штук).
func ReadDaily(rows [][]string) (domain.DailySales, DailyInfo, error) {
	byDay := map[time.Time]float64{}
	info, err := readWide(rows, byDay)
	if errors.Is(err, ErrNoDaily) {
		info, err = readLong(rows, byDay)
	}
	if err != nil {
		return domain.DailySales{}, DailyInfo{}, err
	}
	ds := make([]domain.DaySales, 0, len(byDay))
	for d, v := range byDay {
		ds = append(ds, domain.DaySales{Day: d, Value: v})
	}
	s, err := domain.NewDailySales(ds)
	if err != nil {
		return domain.DailySales{}, DailyInfo{}, err
	}
	days := s.Days()
	if len(days) == 0 {
		return domain.DailySales{}, DailyInfo{}, ErrNoDaily
	}
	info.Days, info.From, info.To = len(days), days[0].Day, days[len(days)-1].Day
	return s, info, nil
}

func readWide(rows [][]string, byDay map[time.Time]float64) (DailyInfo, error) {
	for h := 0; h < len(rows) && h < headerScan; h++ {
		cols := map[int]time.Time{}
		for i, c := range rows[h] {
			if t, ok := parseDay(c); ok {
				cols[i] = t
			}
		}
		if len(cols) < minDayColumns {
			continue
		}
		info := DailyInfo{Format: DailyWide}
		for r := h + 1; r < len(rows); r++ {
			if blank(rows[r]) || totalRow(rows[r]) {
				continue
			}
			for i, t := range cols {
				v, _, empty, err := parseNumber(cell(rows[r], i))
				if err != nil {
					return info, fmt.Errorf("строка %d, «%s»: %w «%s»", r+1, rows[h][i], ErrNotANumber, strings.TrimSpace(cell(rows[r], i)))
				}
				if !empty {
					byDay[t] += v
				}
			}
			info.Products++
		}
		return info, nil
	}
	return DailyInfo{}, ErrNoDaily
}

var (
	dateHeads = []string{"дата", "день", "date"}
	qtyHeads  = []string{"количество", "колво", "продажи", "продано", "штук", "шт", "qty", "quantity"}
)

func headIs(h string, prefixes []string) bool {
	f := domain.FoldName(h)
	for _, p := range prefixes {
		if strings.HasPrefix(f, p) {
			return true
		}
	}
	return false
}

func readLong(rows [][]string, byDay map[time.Time]float64) (DailyInfo, error) {
	for h := 0; h < len(rows) && h < headerScan; h++ {
		dateAt, qtyAt := longColumns(rows[h])
		if dateAt < 0 {
			continue
		}
		if qtyAt < 0 {
			return DailyInfo{}, fmt.Errorf("%w: есть колонка «%s», но нет колонки штук (количество, продажи)", ErrNoDaily, rows[h][dateAt])
		}
		for r := h + 1; r < len(rows); r++ {
			t, ok := parseDay(cell(rows[r], dateAt))
			if !ok {
				continue // итог, пустая строка, подпись
			}
			v, _, empty, err := parseNumber(cell(rows[r], qtyAt))
			if err != nil {
				return DailyInfo{}, fmt.Errorf("строка %d, «%s»: %w «%s»", r+1, rows[h][qtyAt], ErrNotANumber, strings.TrimSpace(cell(rows[r], qtyAt)))
			}
			if !empty {
				byDay[t] += v
			}
		}
		return DailyInfo{Format: DailyLong}, nil
	}
	return DailyInfo{}, ErrNoDaily
}

// longColumns finds the date and quantity columns of a long file's header; -1 — нет.
func longColumns(header []string) (dateAt, qtyAt int) {
	dateAt, qtyAt = -1, -1
	for i, c := range header {
		switch {
		case dateAt < 0 && headIs(c, dateHeads):
			dateAt = i
		case qtyAt < 0 && headIs(c, qtyHeads):
			qtyAt = i
		}
	}
	return dateAt, qtyAt
}

// totalRow — строка итога: «Итого» или «Всего» в одной из первых ячеек.
func totalRow(row []string) bool {
	for i := 0; i < len(row) && i < 3; i++ {
		if isTotal(row[i]) {
			return true
		}
	}
	return false
}
