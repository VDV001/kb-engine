// Package planxlsx writes the sales plan to Excel (#580): листы «По неделям» и
// «По дням», числа — числами, чтобы Excel и Numbers считали их сами.
package planxlsx

import (
	"math"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/domain"
)

const (
	weeksSheet = "По неделям"
	daysSheet  = "По дням"
)

// Write saves the plan to path.
func Write(path string, p domain.Plan) (err error) {
	f := excelize.NewFile()
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if err := f.SetSheetName("Sheet1", weeksSheet); err != nil {
		return err
	}
	if _, err := f.NewSheet(daysSheet); err != nil {
		return err
	}
	if err := writeRows(f, weeksSheet, []any{"Неделя с", "Дней", "Спрос, шт", "Продать, шт", "Остаток на конец, шт", "Цена, ₽", "Прибыль, ₽", "Пометки"}, weekRows(p)); err != nil {
		return err
	}
	if err := writeRows(f, daysSheet, []any{"День", "Спрос, шт", "Продать, шт", "Остаток на конец, шт", "Цена, ₽", "Прибыль, ₽"}, dayRows(p)); err != nil {
		return err
	}
	return f.SaveAs(path)
}

func weekRows(p domain.Plan) [][]any {
	var out [][]any
	for _, w := range p.Weeks() {
		var marks []string
		if w.Peak {
			marks = append(marks, "пик")
		}
		if w.Dip {
			marks = append(marks, "провал")
		}
		if w.Short {
			marks = append(marks, "не хватит остатка")
		}
		out = append(out, []any{w.From, w.Days, round(w.Demand), round(w.Sell), round(w.StockEnd), round(w.Price), round(w.Profit), strings.Join(marks, ", ")})
	}
	return out
}

func dayRows(p domain.Plan) [][]any {
	var out [][]any
	for _, d := range p.Days() {
		out = append(out, []any{d.Day, round(d.Demand), round(d.Sell), round(d.StockEnd), round(d.Price), round(d.Profit)})
	}
	return out
}

// round — штуки и рубли целыми: прогноз дробный, а продают и платят целыми.
// ponytail: сумма округленных строк может разойтись с итогом плана на единицы;
// потолок — сверка итога в Excel до штуки; путь — итог отдельной строкой.
func round(v float64) float64 { return math.Round(v) }

func writeRows(f *excelize.File, sheet string, head []any, rows [][]any) error {
	if err := f.SetSheetRow(sheet, "A1", &head); err != nil {
		return err
	}
	for i, r := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			return err
		}
		if err := f.SetSheetRow(sheet, cell, &r); err != nil {
			return err
		}
	}
	// Даты — датами с видом «ДД.ММ.ГГГГ»: Excel сортирует и строит по ним (ревью #599).
	fmtDate := "dd.mm.yyyy"
	style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &fmtDate})
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		last, err := excelize.CoordinatesToCellName(1, len(rows)+1)
		if err != nil {
			return err
		}
		if err := f.SetCellStyle(sheet, "A2", last, style); err != nil {
			return err
		}
	}
	return f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
}
