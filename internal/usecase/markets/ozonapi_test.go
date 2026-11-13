package markets_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestOzonShop(t *testing.T) {
	// Выдуманный кабинет: брюки продаются и кончились, футболка в пути,
	// пижама лежит без заказов. Отправления — как их отдает API Ozon.
	trousers := markets.OzonItem{SKU: "11", Article: "100-16_46", Name: "Брюки тестовые", Qty: 1, Price: 1000}
	var posts []markets.OzonPosting
	for range 6 {
		posts = append(posts, markets.OzonPosting{Scheme: "FBO", Status: "delivered", Items: []markets.OzonItem{trousers}})
	}
	posts = append(posts,
		markets.OzonPosting{Scheme: "FBO", Status: "cancelled", CancelReason: "Покупатель отказался при вручении: товар не подошел", Items: []markets.OzonItem{trousers}},
		markets.OzonPosting{Scheme: "FBO", Status: "cancelled", CancelReason: "Покупатель отменил заказ: нашел дешевле", Items: []markets.OzonItem{trousers}},
		markets.OzonPosting{Scheme: "FBS", Status: "delivering", Items: []markets.OzonItem{{SKU: "22", Article: "200_L", Name: "Футболка тестовая", Qty: 2, Price: 500}}},
	)
	stock := []markets.OzonStock{{SKU: "11", Article: "100-16_46", Qty: 0}, {SKU: "22", Article: "200_L", Qty: 30}, {SKU: "33", Article: "300_M", Name: "Пижама тестовая", Qty: 5}, {SKU: "44", Article: "400_S", Qty: 1}}
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	r, err := markets.OzonShop(posts, stock, from, from.AddDate(0, 0, 89))
	if err != nil {
		t.Fatalf("OzonShop: %v", err)
	}
	if r.Period != "15.08.2026 – 12.11.2026" || r.PeriodDays != 90 {
		t.Errorf("period = %q, %d days", r.Period, r.PeriodDays)
	}
	if len(r.Products) != 4 {
		t.Fatalf("products = %d, want 4 (the ones without orders too)", len(r.Products))
	}
	a := r.Products[0].Data()
	if a.SKU != "11" || a.Name != "Брюки тестовые" || a.Article != "100-16_46" || a.Scheme != "FBO" ||
		a.Ordered != 8 || a.Delivered != 6 || a.BoughtOut != 6 || a.Cancelled != 2 || a.Refused != 1 || a.OrderedSum != 8000 || a.AvgPrice != 1000 {
		t.Errorf("trousers = %+v", a)
	}
	// Продано 6 за 90 дней, остатка нет: срочно, на 4 недели — 2 шт.
	if !a.UrgentSupply || a.SupplyQty != 2 {
		t.Errorf("trousers supply: urgent %v, qty %v; want true, 2", a.UrgentSupply, a.SupplyQty)
	}
	b := r.Products[1].Data()
	if b.SKU != "22" || b.Scheme != "FBS" || b.Ordered != 2 || b.Stock != 30 || b.UrgentSupply {
		t.Errorf("t-shirt = %+v", b)
	}
	if c := r.Products[2].Data(); c.SKU != "33" || c.Name != "Пижама тестовая" || c.Ordered != 0 || c.Stock != 5 {
		t.Errorf("no orders = %+v; the card names it", c)
	}
	if d := r.Products[3].Data(); d.Name != "400_S" {
		t.Errorf("no orders, no card = %+v; the article names it", d)
	}
	if tot := r.Total.Data(); tot.Ordered != 10 || tot.Cancelled != 2 || tot.Refused != 1 || tot.Stock != 36 || tot.OrderedSum != 9000 {
		t.Errorf("total = %+v", tot)
	}
}

func TestOzonShop_badPeriod(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	if _, err := markets.OzonShop(nil, nil, now, now.AddDate(0, 0, -1)); err == nil {
		t.Error("period backwards read without error")
	}
}
