package domain_test

import (
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func shop(t *testing.T, ds ...domain.ShopProductData) []*domain.ShopProduct {
	t.Helper()
	var out []*domain.ShopProduct
	for _, d := range ds {
		p, err := domain.NewShopProduct(d)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestAdviseShop(t *testing.T) {
	// Выдуманный магазин: брюки отказывают на примерке, пижама кончается,
	// футболка лежит, вторая пижама продается чисто.
	ps := shop(t,
		domain.ShopProductData{Name: "Брюки", SKU: "1", Ordered: 40, Cancelled: 24, Refused: 22, Stock: 50},
		domain.ShopProductData{Name: "Брюки другой размер", SKU: "2", Ordered: 12, Cancelled: 6, Refused: 5, Stock: 20},
		domain.ShopProductData{Name: "Пижама", SKU: "3", Ordered: 30, Cancelled: 4, Refused: 3, Stock: 0, UrgentSupply: true, SupplyQty: 9},
		domain.ShopProductData{Name: "Футболка", SKU: "4", Stock: 75},
		domain.ShopProductData{Name: "Пижама чистая", SKU: "5", Ordered: 25, Cancelled: 2, Refused: 1, Stock: 40},
		domain.ShopProductData{Name: "Мало заказов", SKU: "6", Ordered: 3, Cancelled: 3, Refused: 3, Stock: 1},
	)
	got := domain.AdviseShop(ps)
	kinds := make([]domain.ShopAdviceKind, len(got))
	for i, a := range got {
		kinds[i] = a.Kind
	}
	want := []domain.ShopAdviceKind{domain.AdviceRefusals, domain.AdviceSupply, domain.AdviceDeadStock, domain.AdviceKeep}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	// Отказы: 40 % и больше при 10+ заказах, больше всего отказов — первым; «Мало заказов» не в счет.
	if a := got[0]; !slices.Equal(a.SKUs, []string{"1", "2"}) || a.Units != 27 {
		t.Errorf("refusals = %+v", a)
	}
	if a := got[1]; !slices.Equal(a.SKUs, []string{"3"}) || a.Units != 9 {
		t.Errorf("supply = %+v", a)
	}
	if a := got[2]; !slices.Equal(a.SKUs, []string{"4"}) || a.Units != 75 {
		t.Errorf("dead stock = %+v", a)
	}
	// Держать в наличии: 10+ заказов, отказы и отмены низкие.
	if a := got[3]; !slices.Equal(a.SKUs, []string{"3", "5"}) {
		t.Errorf("keep = %+v", a)
	}
	if len(domain.AdviseShop(nil)) != 0 {
		t.Error("empty shop got advice")
	}
}

func TestAdviseShop_keepNeedsNoIssues(t *testing.T) {
	// В отчете-файле отказов нет (0), зато есть возвраты, ДРР и рейтинг:
	// «продается чисто» — только без проблем по ним.
	ps := shop(t,
		domain.ShopProductData{Name: "Возвращают", SKU: "1", Ordered: 20, BoughtOut: 8, Cancelled: 2, Returned: 10},
		domain.ShopProductData{Name: "Дорогая реклама", SKU: "2", Ordered: 20, BoughtOut: 18, Cancelled: 2, DRR: 0.4},
		domain.ShopProductData{Name: "Чисто", SKU: "3", Ordered: 20, BoughtOut: 18, Cancelled: 2},
	)
	for _, a := range domain.AdviseShop(ps) {
		if a.Kind == domain.AdviceKeep && !slices.Equal(a.SKUs, []string{"3"}) {
			t.Errorf("keep = %v, want only the clean one", a.SKUs)
		}
	}
}

func TestSupply(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sold, stock float64
		days        int
		urgent      bool
		qty         float64
	}{
		{"кончается", 6, 0, 90, true, 2},
		{"хватает", 90, 30, 90, false, 0},
		{"мало продаж — темп случаен", 4, 0, 90, false, 0},
		{"нет периода", 10, 0, 0, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if u, q := domain.Supply(tc.sold, tc.stock, tc.days); u != tc.urgent || q != tc.qty {
				t.Errorf("Supply = %v, %v; want %v, %v", u, q, tc.urgent, tc.qty)
			}
		})
	}
}
