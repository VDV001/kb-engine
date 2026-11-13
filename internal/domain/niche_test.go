package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestRecognizeNicheColumn(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   domain.NicheColumn
		ok     bool
	}{
		{"Ниша", domain.NicheName, true},
		{"Предмет", domain.NicheName, true},
		{"Категория", domain.NicheName, true},
		{"Продажи, шт", domain.NicheSales, true},
		{"Выручка, ₽", domain.NicheRevenue, true},
		{"Продавцы", domain.NicheSellers, true},
		{"Продавцов с продажами", domain.NicheSellers, true},
		{"Средняя цена, ₽", domain.NichePrice, true},
		{"Средний чек", domain.NichePrice, true},
		{"Рост к году, %", domain.NicheGrowth, true},
		{"Доля топ-10, %", domain.NicheTop10, true},
		{"Выручка топ-10, %", domain.NicheTop10, true},
		{"  СРЕДНЯЯ  ЦЕНА ", domain.NichePrice, true},
		// Похоже на выручку, но не она: упущенная выручка — другая величина.
		{"Упущенная выручка", "", false},
		{"Остатки", "", false},
		{"", "", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			got, ok := domain.RecognizeNicheColumn(tc.header)
			if ok != tc.ok || got != tc.want {
				t.Errorf("RecognizeNicheColumn(%q) = %q, %v; want %q, %v", tc.header, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseNicheColumn(t *testing.T) {
	for _, c := range domain.NicheColumns() {
		got, err := domain.ParseNicheColumn(string(c))
		if err != nil || got != c {
			t.Errorf("ParseNicheColumn(%q) = %q, %v", c, got, err)
		}
		if c.Label() == "" {
			t.Errorf("column %q has no label", c)
		}
	}
	if _, err := domain.ParseNicheColumn("stock"); !errors.Is(err, domain.ErrUnknownNicheColumn) {
		t.Errorf("unknown column: err = %v, want ErrUnknownNicheColumn", err)
	}
}

func TestNewNiche(t *testing.T) {
	ok := map[domain.NicheColumn]float64{domain.NicheSales: 182000, domain.NichePrice: 7490, domain.NicheGrowth: 0.12, domain.NicheTop10: 0.38}
	n, err := domain.NewNiche("  Куртки зимние мужские ", ok)
	if err != nil {
		t.Fatalf("NewNiche: %v", err)
	}
	if n.Name() != "Куртки зимние мужские" {
		t.Errorf("Name() = %q", n.Name())
	}
	if v, has := n.Value(domain.NicheSales); !has || v != 182000 {
		t.Errorf("Value(sales) = %v, %v", v, has)
	}
	if _, has := n.Value(domain.NicheSellers); has {
		t.Error("Value(sellers) present, but the file had no such column")
	}
	ok[domain.NicheSales] = 1 // ниша не делит карту с вызывающим
	if v, _ := n.Value(domain.NicheSales); v != 182000 {
		t.Errorf("niche changed with the caller's map: sales = %v", v)
	}

	for _, tc := range []struct {
		name string
		in   string
		vals map[domain.NicheColumn]float64
	}{
		{"пустое имя", "  ", nil},
		{"продажи меньше нуля", "x", map[domain.NicheColumn]float64{domain.NicheSales: -1}},
		{"цена меньше нуля", "x", map[domain.NicheColumn]float64{domain.NichePrice: -5}},
		{"продавцов меньше нуля", "x", map[domain.NicheColumn]float64{domain.NicheSellers: -2}},
		{"доля топ-10 больше 1", "x", map[domain.NicheColumn]float64{domain.NicheTop10: 1.2}},
		{"падение больше 100 %", "x", map[domain.NicheColumn]float64{domain.NicheGrowth: -1.5}},
		{"имя как значение", "x", map[domain.NicheColumn]float64{domain.NicheName: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewNiche(tc.in, tc.vals); !errors.Is(err, domain.ErrInvalidNiche) {
				t.Errorf("err = %v, want ErrInvalidNiche", err)
			}
		})
	}
}
