package markets_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Выдуманные ниши в форме выгрузки сервиса: заголовок не в первой строке,
// числа с неразрывными пробелами и знаками валюты.
var export = [][]string{
	{"Выгрузка ниш за 12 месяцев"},
	{},
	{"Предмет", "Продажи, шт", "Выручка, ₽", "Продавцы", "Средняя цена, ₽", "Рост к году, %", "Доля топ-10, %", "Остатки"},
	{"Куртки зимние мужские", "182 000", "1 363 180 000 ₽", "1840", "7 490", "12%", "38 %", "51 000"},
	{"Термобелье детское", "96000", "181440000", "640", "1890,50", "31%", "29%", ""},
	{"Итого", "278000", "1544620000", "", "", "", "", ""},
	{"", "", "", "", "", "", "", ""},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestReadNiches_export(t *testing.T) {
	tb, err := markets.ReadNiches(export, nil)
	if err != nil {
		t.Fatalf("ReadNiches: %v", err)
	}
	if tb.HeaderRow != 3 {
		t.Errorf("HeaderRow = %d, want 3 (строки файла с 1)", tb.HeaderRow)
	}
	want := []domain.NicheColumn{domain.NicheName, domain.NicheSales, domain.NicheRevenue, domain.NicheSellers, domain.NichePrice, domain.NicheGrowth, domain.NicheTop10, ""}
	if len(tb.Columns) != len(want) {
		t.Fatalf("Columns = %d, want %d", len(tb.Columns), len(want))
	}
	for i, c := range tb.Columns {
		if c.Index != i || c.Field != want[i] || c.Header != export[2][i] {
			t.Errorf("Columns[%d] = %+v, want field %q", i, c, want[i])
		}
	}
	if len(tb.Niches) != 2 {
		t.Fatalf("Niches = %d, want 2 (итог и пустая строка — не ниши)", len(tb.Niches))
	}
	n := tb.Niches[0]
	for col, v := range map[domain.NicheColumn]float64{
		domain.NicheSales: 182000, domain.NicheRevenue: 1363180000, domain.NicheSellers: 1840,
		domain.NichePrice: 7490, domain.NicheGrowth: 0.12, domain.NicheTop10: 0.38,
	} {
		if got, ok := n.Value(col); !ok || !near(got, v) {
			t.Errorf("%s %s = %v, %v; want %v", n.Name(), col, got, ok, v)
		}
	}
	if got, _ := tb.Niches[1].Value(domain.NichePrice); !near(got, 1890.5) {
		t.Errorf("price with decimal comma = %v, want 1890.5", got)
	}
	if len(tb.Problems) != 0 {
		t.Errorf("Problems = %+v, want none", tb.Problems)
	}
}

func TestReadNiches_percentColumnWithoutSign(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cells []string
		want  []float64
	}{
		{"проценты без знака", []string{"12", "-6", "0,5"}, []float64{0.12, -0.06, 0.005}},
		{"доли", []string{"0,12", "-0.06", "0.5"}, []float64{0.12, -0.06, 0.5}},
		{"со знаком", []string{"12%", "-6 %", "0,5%"}, []float64{0.12, -0.06, 0.005}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := [][]string{{"Ниша", "Рост"}}
			for i, c := range tc.cells {
				rows = append(rows, []string{string(rune('a' + i)), c})
			}
			tb, err := markets.ReadNiches(rows, nil)
			if err != nil {
				t.Fatalf("ReadNiches: %v", err)
			}
			if len(tb.Niches) != len(tc.want) {
				t.Fatalf("Niches = %d, want %d", len(tb.Niches), len(tc.want))
			}
			for i, n := range tb.Niches {
				if got, _ := n.Value(domain.NicheGrowth); !near(got, tc.want[i]) {
					t.Errorf("row %d growth = %v, want %v", i, got, tc.want[i])
				}
			}
		})
	}
}

func TestReadNiches_unknownAndPicked(t *testing.T) {
	rows := [][]string{
		{"Товарная группа", "Продажи", "Продавцы", "Продавцы с продажами"},
		{"Варежки вязаные", "58000", "520", "410"},
	}
	_, err := markets.ReadNiches(rows, nil)
	if !errors.Is(err, markets.ErrNoNameColumn) {
		t.Fatalf("err = %v, want ErrNoNameColumn", err)
	}
	tb, _ := markets.ReadNiches(rows, nil)
	if len(tb.Columns) != 4 || tb.Columns[0].Field != "" {
		t.Fatalf("columns must come back so the window can ask: %+v", tb.Columns)
	}
	if tb.Columns[3].Field != "" {
		t.Errorf("second «продавцы» column = %q, want unknown: одно поле — одна колонка", tb.Columns[3].Field)
	}

	tb, err = markets.ReadNiches(rows, map[int]domain.NicheColumn{0: domain.NicheName, 2: "", 3: domain.NicheSellers})
	if err != nil {
		t.Fatalf("with picks: %v", err)
	}
	if tb.Columns[2].Field != "" || tb.Columns[3].Field != domain.NicheSellers {
		t.Errorf("picks not applied: %+v", tb.Columns)
	}
	if v, _ := tb.Niches[0].Value(domain.NicheSellers); v != 410 {
		t.Errorf("sellers = %v, want 410 from the picked column", v)
	}

	_, err = markets.ReadNiches(rows, map[int]domain.NicheColumn{0: domain.NicheName, 1: domain.NicheSales, 2: domain.NicheSales})
	if !errors.Is(err, markets.ErrDuplicatePick) {
		t.Errorf("same field twice: err = %v, want ErrDuplicatePick", err)
	}
	_, err = markets.ReadNiches(rows, map[int]domain.NicheColumn{9: domain.NicheName})
	if !errors.Is(err, markets.ErrNoSuchColumn) {
		t.Errorf("pick outside the table: err = %v, want ErrNoSuchColumn", err)
	}
}

func TestReadNiches_badRowsAreNamed(t *testing.T) {
	rows := [][]string{
		{"Ниша", "Продажи", "Доля топ-10"},
		{"Гирлянды", "много", "22%"},
		{"Адвент-календари", "34000", "147%"},
		{"Варежки", "58000", "18%"},
	}
	tb, err := markets.ReadNiches(rows, nil)
	if err != nil {
		t.Fatalf("ReadNiches: %v", err)
	}
	if len(tb.Niches) != 1 || tb.Niches[0].Name() != "Варежки" {
		t.Errorf("Niches = %d, want only «Варежки»", len(tb.Niches))
	}
	if len(tb.Problems) != 2 || tb.Problems[0].Row != 2 || tb.Problems[1].Row != 3 {
		t.Fatalf("Problems = %+v, want rows 2 and 3", tb.Problems)
	}
	for _, p := range tb.Problems {
		if p.Reason == "" {
			t.Errorf("row %d has no reason", p.Row)
		}
	}
}

func TestReadNiches_empty(t *testing.T) {
	for _, rows := range [][][]string{nil, {{}, {"", " "}}} {
		if _, err := markets.ReadNiches(rows, nil); !errors.Is(err, markets.ErrEmptyFile) {
			t.Errorf("rows %q: err = %v, want ErrEmptyFile", rows, err)
		}
	}
}

func TestReadNiches_monthsAndMarketplaces(t *testing.T) {
	rows := [][]string{
		{"Ниша", "Маркетплейс", "Продажи", "ноя 2025", "дек 2025", "янв 2026", "фев 2026", "мар 2026", "апр 2026"},
		{"Куртки", "WB, Ozon", "182000", "9 000", "12 000", "5 000", "3 000", "", "1 000"},
		{"Варежки", "озон", "58000", "", "", "", "", "", ""},
		{"Гирлянды", "Авито", "230000", "1", "2", "3", "4", "5", "6"},
	}
	tb, err := markets.ReadNiches(rows, nil)
	if err != nil {
		t.Fatalf("ReadNiches: %v", err)
	}
	if c := tb.Columns[3]; c.Month != time.November || c.Year != 2025 || c.Field != "" {
		t.Errorf("month column = %+v", c)
	}
	if tb.Columns[1].Field != domain.NicheMarketplace {
		t.Errorf("marketplace column = %+v", tb.Columns[1])
	}
	if len(tb.Niches) != 2 {
		t.Fatalf("Niches = %d, want 2 (Авито — проблема)", len(tb.Niches))
	}
	j := tb.Niches[0]
	if got := j.Marketplaces(); len(got) != 2 {
		t.Errorf("marketplaces = %v", got)
	}
	ms := j.Months()
	if len(ms) != 5 || ms[0].Month != time.November || ms[0].Value != 9000 || ms[4].Month != time.April {
		t.Errorf("months = %+v; пустая ячейка — месяца нет", ms)
	}
	if len(tb.Niches[1].Months()) != 0 {
		t.Errorf("months without values = %+v", tb.Niches[1].Months())
	}
	if len(tb.Problems) != 1 || tb.Problems[0].Row != 4 {
		t.Errorf("Problems = %+v, want row 4", tb.Problems)
	}
}
