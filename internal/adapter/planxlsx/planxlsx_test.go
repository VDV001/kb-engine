package planxlsx_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/adapter/planxlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

func plan(t *testing.T) domain.Plan {
	t.Helper()
	d := func(m time.Month, day int) time.Time { return time.Date(2026, m, day, 0, 0, 0, 0, time.UTC) }
	mk := func(from, to time.Time, v float64) []domain.DaySales {
		var out []domain.DaySales
		for x := from; !x.After(to); x = x.AddDate(0, 0, 1) {
			out = append(out, domain.DaySales{Day: x, Value: v})
		}
		return out
	}
	lastYear, err := domain.NewDailySales(append(mk(time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 10, 31, 0, 0, 0, 0, time.UTC), 100),
		mk(time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 11, 14, 0, 0, 0, 0, time.UTC), 200)...))
	if err != nil {
		t.Fatal(err)
	}
	now, _ := domain.NewDailySales(mk(d(9, 1), d(10, 31), 100))
	own, _ := domain.NewDailySales(mk(d(9, 1), d(10, 31), 10))
	f, err := domain.NewForecast(lastYear, now, own)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.NewPlan(f, domain.PlanInput{Stock: 100, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Файл открывается тем, чем его откроет человек (excelize читает OOXML, как
// Excel и Numbers): два листа, шапка, строка на неделю и на день, числа — числами.
func TestWrite(t *testing.T) {
	p := plan(t)
	path := filepath.Join(t.TempDir(), "план.xlsx")
	if err := planxlsx.Write(path, p); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("written file does not open: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := f.GetSheetList(); len(got) != 2 || got[0] != "По неделям" || got[1] != "По дням" {
		t.Fatalf("sheets = %v", got)
	}
	weeks, _ := f.GetRows("По неделям")
	if len(weeks) != len(p.Weeks())+1 || weeks[0][0] != "Неделя с" || weeks[0][len(weeks[0])-1] != "Пометки" {
		t.Fatalf("weeks sheet = %v", weeks)
	}
	days, _ := f.GetRows("По дням")
	if len(days) != len(p.Days())+1 || days[0][0] != "День" {
		t.Fatalf("days sheet: %d rows, want %d", len(days), len(p.Days())+1)
	}
	// Продано на первой неделе — число в ячейке, а не текст.
	if typ, _ := f.GetCellType("По неделям", "D2"); typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
		t.Errorf("sell cell type = %v, want a number", typ)
	}
	// Прогноз 20 шт/день (прошлый год ×2, рост 1, доля 10 %); первая неделя —
	// одно воскресенье 01.11: продать 20.
	if v, _ := f.GetCellValue("По неделям", "D2", excelize.Options{RawCellValue: true}); v != "20" {
		t.Errorf("week 1 sell = %q, want 20", v)
	}
}
