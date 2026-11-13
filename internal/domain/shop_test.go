package domain_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewShopProduct(t *testing.T) {
	// Выдуманный товар: 100 заказано, 30 выкуплено, 65 отменено, 5 возвращено.
	p, err := domain.NewShopProduct(domain.ShopProductData{
		Name: " Куртка тестовая ", SKU: "1001", Ordered: 100, Delivered: 35, BoughtOut: 30, Cancelled: 65, Returned: 5,
		Stock: 3, DaysOut: 10, DaysOutOf: 28, UrgentSupply: true, Rating: 4.2, Reviews: 7, DRR: 0.2, AvgPrice: 4500,
	})
	if err != nil {
		t.Fatalf("NewShopProduct: %v", err)
	}
	if p.Name() != "Куртка тестовая" || p.Data().SKU != "1001" {
		t.Errorf("name = %q", p.Name())
	}
	if p.BuyoutRate() != 0.3 || p.CancelRate() != 0.65 || p.ReturnRate() != 0.05 {
		t.Errorf("rates = %v %v %v", p.BuyoutRate(), p.CancelRate(), p.ReturnRate())
	}
	want := []domain.ShopIssue{domain.IssueCancels, domain.IssueStock, domain.IssueAds, domain.IssueRating}
	if got := p.Issues(); !slices.Equal(got, want) {
		t.Errorf("issues = %v, want %v", got, want)
	}

	calm, _ := domain.NewShopProduct(domain.ShopProductData{Name: "Тихий", Ordered: 10, BoughtOut: 9, Cancelled: 1, Rating: 5, Reviews: 3})
	if len(calm.Issues()) != 0 {
		t.Errorf("calm issues = %v", calm.Issues())
	}
	none, _ := domain.NewShopProduct(domain.ShopProductData{Name: "Без заказов"})
	if none.BuyoutRate() != 0 || len(none.Issues()) != 0 {
		t.Errorf("no orders: rate %v, issues %v", none.BuyoutRate(), none.Issues())
	}
	unrated, _ := domain.NewShopProduct(domain.ShopProductData{Name: "Без отзывов", Ordered: 5, BoughtOut: 5, Rating: 0, Reviews: 0})
	if slices.Contains(unrated.Issues(), domain.IssueRating) {
		t.Error("rating 0 without reviews is no rating, not a bad one")
	}

	for name, d := range map[string]domain.ShopProductData{
		"без названия":         {Ordered: 1},
		"заказано меньше нуля": {Name: "x", Ordered: -1},
		"выкуплено больше":     {Name: "x", Ordered: 5, BoughtOut: 6},
		"рейтинг больше 5":     {Name: "x", Rating: 5.5},
		"дней без остатка":     {Name: "x", DaysOut: 30, DaysOutOf: 28},
		"ДРР больше 100 %":     {Name: "x", DRR: 1.5},
	} {
		if _, err := domain.NewShopProduct(d); !errors.Is(err, domain.ErrInvalidShopProduct) {
			t.Errorf("%s: err = %v, want ErrInvalidShopProduct", name, err)
		}
	}
}
