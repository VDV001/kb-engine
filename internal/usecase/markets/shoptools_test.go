package markets_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Выдуманный кабинет на 4 недели: брюки продавались и кончились, худи
// отказывают при получении, пижама лежит без заказов.
func shopData() markets.ShopData {
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) // понедельник
	to := from.AddDate(0, 0, 27)
	var posts []markets.OzonPosting
	add := func(day int, status, reason string, it markets.OzonItem) {
		posts = append(posts, markets.OzonPosting{Scheme: "FBO", Status: status, CancelReason: reason, At: from.AddDate(0, 0, day).Add(10 * time.Hour), Items: []markets.OzonItem{it}})
	}
	trousers := markets.OzonItem{SKU: "11", Article: "100_46", Name: "Брюки тестовые", Qty: 1, Price: 3000}
	hoodie := markets.OzonItem{SKU: "22", Article: "200_L", Name: "Худи тестовое", Qty: 1, Price: 5000}
	for d := range 14 { // две недели по брюкам в день, потом остаток кончился
		add(d, "delivered", "", trousers)
	}
	for d := range 12 {
		reason, status := "", "delivered"
		if d%2 == 0 {
			status, reason = "cancelled", "Покупатель отказался при вручении: товар не подошел"
		}
		add(d*2, status, reason, hoodie)
	}
	stock := []markets.OzonStock{{SKU: "11", Article: "100_46", Qty: 0}, {SKU: "22", Article: "200_L", Qty: 30}, {SKU: "33", Article: "300_M", Name: "Пижама тестовая", Qty: 40}}
	return markets.ShopData{Name: "Тест", Posts: posts, Stock: stock, From: from, To: to, Loc: time.UTC}
}

func run(t *testing.T, name, args string) string {
	t.Helper()
	tools := markets.ShopTools{Load: func(context.Context, string) (markets.ShopData, error) { return shopData(), nil }}
	out, err := tools.Run(context.Background(), name, []byte(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestShopTools(t *testing.T) {
	specs := markets.ShopTools{}.Specs()
	if len(specs) != 4 {
		t.Fatalf("specs = %d, want summary, top, supply, sku", len(specs))
	}
	for _, s := range specs {
		if !strings.HasPrefix(s.Name, "shop_") || s.Description == "" || !strings.HasPrefix(s.Params, "{") {
			t.Errorf("spec %+v", s)
		}
	}
	sum := run(t, "shop_summary", `{}`)
	for _, want := range []string{"Тест", "19.10.2026", "заказано 26 шт на 102 000 ₽", "с отменами", "выкуплено 20 шт на 72 000 ₽", "отказ при получении 6 шт", "довезти: 1", "лежит без заказов: 1"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary misses %q:\n%s", want, sum)
		}
	}
	// «Худшие по отказам» — где отказов больше всего (ревью #593).
	for _, order := range []string{"worst", ""} {
		top := run(t, "shop_top", `{"by":"refused","order":"`+order+`","n":1}`)
		if !strings.Contains(top, "Худи тестовое") || strings.Contains(top, "Брюки") {
			t.Errorf("top by refusals, order %q:\n%s", order, top)
		}
	}
	worst := run(t, "shop_top", `{"by":"ordered","order":"worst","n":1}`)
	if !strings.Contains(worst, "Пижама тестовая") {
		t.Errorf("worst by orders (with stock):\n%s", worst)
	}
	sup := run(t, "shop_supply", `{}`)
	if !strings.Contains(sup, "Брюки тестовые") || !strings.Contains(sup, "довезти") || strings.Contains(sup, "Худи") {
		t.Errorf("supply:\n%s", sup)
	}
	sku := run(t, "shop_sku", `{"q":"брюки"}`)
	// По неделям видно, почему упали продажи: остаток кончился на третьей.
	// Заказы — с отменами, как в «Прогнозе» (ревью #593).
	for _, want := range []string{"Брюки тестовые", "по неделям", "заказано с отменами", "19.10", "7 шт", "02.11", "0 шт", "остаток 0"} {
		if !strings.Contains(sku, want) {
			t.Errorf("sku misses %q:\n%s", want, sku)
		}
	}
	if none := run(t, "shop_sku", `{"q":"куртка"}`); !strings.Contains(none, "не найден") {
		t.Errorf("unknown sku: %s", none)
	}
}

func TestShopTools_unknownAndLoadError(t *testing.T) {
	tools := markets.ShopTools{Load: func(context.Context, string) (markets.ShopData, error) {
		return markets.ShopData{}, markets.ErrOzonPeriod
	}}
	if _, err := tools.Run(context.Background(), "shop_summary", []byte(`{}`)); err == nil {
		t.Error("load error swallowed")
	}
	if _, err := (markets.ShopTools{}).Run(context.Background(), "shop_nope", []byte(`{}`)); err == nil {
		t.Error("unknown tool ran")
	}
}

func TestShopTools_partialWeekAndProblems(t *testing.T) {
	// Окно кончается в среду: последняя неделя неполная и так подписана, а
	// не выглядит падением (ревью #593). Товар без названия — назван в сводке.
	d := shopData()
	d.To = d.From.AddDate(0, 0, 16) // среда третьей недели
	d.Stock = append(d.Stock, markets.OzonStock{SKU: "44", Qty: 3})
	tools := markets.ShopTools{Load: func(context.Context, string) (markets.ShopData, error) { return d, nil }}
	// Худи заказывают через день: на неделе с 02.11 до среды — 2 заказа, и
	// заказы после конца окна в нее не попадают.
	sku, _ := tools.Run(context.Background(), "shop_sku", []byte(`{"q":"худи"}`))
	if !strings.Contains(sku, "02.11: 2 шт") || !strings.Contains(sku, "неполная, 3 дн.") {
		t.Errorf("partial week:\n%s", sku)
	}
	sum, _ := tools.Run(context.Background(), "shop_summary", []byte(`{}`))
	if !strings.Contains(sum, "не учтено товаров: 1") {
		t.Errorf("summary hides skipped products:\n%s", sum)
	}
	if strings.Contains(sum, "продается чисто") && strings.Contains(sum, "чисто: 0 товаров, 0 шт") {
		t.Errorf("keep advice with units:\n%s", sum)
	}
}
