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
	// Сумма заказов — с отменами; выкуплено на сумму — только доставленное (не выручка «по заказам»).
	if a.BoughtOutSum != 6000 {
		t.Errorf("trousers bought out for %v, want 6000 (6 delivered, not the 2 cancelled)", a.BoughtOutSum)
	}
	if tot := r.Total.Data(); tot.Ordered != 10 || tot.Cancelled != 2 || tot.Refused != 1 || tot.Stock != 36 || tot.OrderedSum != 9000 || tot.BoughtOutSum != 6000 {
		t.Errorf("total = %+v", tot)
	}
}

func TestOzonShop_badPeriod(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	if _, err := markets.OzonShop(nil, nil, now, now.AddDate(0, 0, -1)); err == nil {
		t.Error("period backwards read without error")
	}
}

func TestOzonShop_daysAcrossDST(t *testing.T) {
	// Окно — 90 календарных дней и тогда, когда в них переход на летнее время.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	to := time.Date(2026, 4, 30, 0, 0, 0, 0, berlin)
	r, err := markets.OzonShop(nil, nil, to.AddDate(0, 0, -89), to)
	if err != nil || r.PeriodDays != 90 {
		t.Errorf("days = %d (%v), want 90", r.PeriodDays, err)
	}
}

func TestOzonDaily(t *testing.T) {
	// Свои заказы по дням для «Прогноза» (#580): штуки ВКЛЮЧАЯ отмены — у
	// свежих отправлений отмены еще не закрылись, и ряд «без отмен» завышал бы
	// старые дни против новых (ревью #592); по дню создания в часовом поясе
	// продавца; день без заказов — ноль; отправление без даты — в счетчик.
	msk := time.FixedZone("MSK", 3*3600)
	at := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	item := []markets.OzonItem{{SKU: "1", Qty: 2}}
	posts := []markets.OzonPosting{
		{Status: "delivered", At: at(10, 9), Items: item},
		{Status: "delivering", At: at(10, 22), Items: item}, // 01:00 11-го по Москве
		{Status: "cancelled", At: at(10, 10), Items: item},
		{Status: "delivered", At: at(20, 10), Items: item}, // вне окна
		{Status: "delivered", Items: item},                 // без даты
	}
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, msk)
	s, undated, err := markets.OzonDaily(posts, from, from.AddDate(0, 0, 2), msk)
	if err != nil {
		t.Fatal(err)
	}
	if undated != 1 {
		t.Errorf("undated = %d, want 1", undated)
	}
	var got []float64
	for _, d := range s.Days() {
		got = append(got, d.Value)
	}
	if len(got) != 3 || got[0] != 4 || got[1] != 2 || got[2] != 0 {
		t.Errorf("days = %v, want [4 2 0]", got)
	}
}

// Резерв больше наличия не уменьшает остаток других товаров.
func TestOzonStockTotal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        []markets.OzonStock
		qty       float64
		withStock int
	}{
		{"пусто", nil, 0, 0},
		{"два товара", []markets.OzonStock{{Qty: 5}, {Qty: 7}}, 12, 2},
		{"резерв больше наличия", []markets.OzonStock{{Qty: -3}, {Qty: 10}}, 10, 1},
		{"нулевой", []markets.OzonStock{{Qty: 0}, {Qty: 4}}, 4, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if q, n := markets.OzonStockTotal(tc.in); q != tc.qty || n != tc.withStock {
				t.Errorf("OzonStockTotal = %v, %d; want %v, %d", q, n, tc.qty, tc.withStock)
			}
		})
	}
}
