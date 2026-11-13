package markets_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func d(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }

func TestReadDaily_wide(t *testing.T) {
	// Строки — товары, колонки — дни; над заголовком название отчета, внизу итог.
	rows := [][]string{
		{"Товары ниши «Куртки» по дням (пример)"},
		{"Артикул", "Название", "Продавец", "01.11.2025", "02.11.2025", "2025-11-03", "45965"},
		{"101", "Куртка А", "ИП Иванов", "10", "12", "", "7"},
		{"102", "Куртка Б", "ООО Ромашка", "5", "1 000", "3", "0"},
		{"Итого", "", "", "15", "1012", "3", "7"},
	}
	s, info, err := markets.ReadDaily(rows)
	if err != nil {
		t.Fatalf("ReadDaily: %v", err)
	}
	if info.Format != markets.DailyWide || info.Products != 2 || info.Days != 4 {
		t.Errorf("info = %+v, want wide, 2 products, 4 days", info)
	}
	got := s.Days()
	want := []float64{15, 1012, 3, 7}
	if len(got) != 4 {
		t.Fatalf("days = %+v", got)
	}
	for i, x := range got {
		if !x.Day.Equal(d(2025, 11, i+1)) || x.Value != want[i] {
			t.Errorf("day %d = %v %v, want %v %v", i, x.Day, x.Value, d(2025, 11, i+1), want[i])
		}
	}
	if !info.From.Equal(d(2025, 11, 1)) || !info.To.Equal(d(2025, 11, 4)) {
		t.Errorf("period = %v-%v", info.From, info.To)
	}
}

func TestReadDaily_long(t *testing.T) {
	rows := [][]string{
		{"Дата", "Артикул", "Количество", "Сумма, ₽"},
		{"01.09.26", "101", "2", "1 000"},
		{"01.09.26", "102", "3", "900"},
		{"2026-09-02 14:05:00", "101", "1", "500"},
		{"46268", "101", "4", "800"},
		{"Итого", "", "10", "3200"},
	}
	s, info, err := markets.ReadDaily(rows)
	if err != nil {
		t.Fatalf("ReadDaily: %v", err)
	}
	if info.Format != markets.DailyLong || info.Days != 3 {
		t.Errorf("info = %+v, want long, 3 days", info)
	}
	got := s.Days()
	if len(got) != 3 || got[0].Value != 5 || got[1].Value != 1 || !got[2].Day.Equal(d(2026, 9, 3)) || got[2].Value != 4 {
		t.Errorf("days = %+v, want 01.09 — 5, 02.09 — 1, 03.09 — 4", got)
	}
}

func TestReadDaily_refuses(t *testing.T) {
	for name, rows := range map[string][][]string{
		"пусто":           nil,
		"нет дат":         {{"Ниша", "Продажи"}, {"Куртки", "10"}},
		"дата без штук":   {{"Дата", "Артикул"}, {"01.09.2026", "101"}},
		"штуки не числом": {{"Дата", "Количество"}, {"01.09.2026", "много"}},
	} {
		if _, _, err := markets.ReadDaily(rows); !errors.Is(err, markets.ErrNoDaily) && !errors.Is(err, markets.ErrNotANumber) {
			t.Errorf("%s: err = %v, want ErrNoDaily or ErrNotANumber", name, err)
		}
	}
}
