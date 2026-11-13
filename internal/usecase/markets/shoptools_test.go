package markets_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
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
	if len(specs) != 6 {
		t.Fatalf("specs = %d, want summary, top, supply, sku, money, losses", len(specs))
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
	// У «продается чисто» штук нет: брюки — 14 заказов без отмен.
	if !strings.Contains(sum, "совет — продается чисто: 1 товаров\n") {
		t.Errorf("keep advice must come without units:\n%s", sum)
	}
}

// Деньги и пропажи в чате (#580): те же строки, что «Деньги магазина» и
// «Проверить» в окне; читаются отдельной загрузкой — начисления дорогие.
func TestShopTools_moneyAndLosses(t *testing.T) {
	d := shopData()
	d.Now = d.To.AddDate(0, 0, 1)
	day := d.From.AddDate(0, 0, 3)
	rub := func(r int64) domain.Money { return domain.NewMoney(r * 100) }
	d.Accruals = []markets.OzonAccrual{
		{Day: day, SKU: "11", Unit: "p-1", Kind: markets.AccrualSale, Amount: rub(10000)},
		{Day: day, SKU: "11", Unit: "p-1", Kind: markets.AccrualCommission, Amount: rub(-4000)},
		{Day: day, SKU: "11", Unit: "p-1", Kind: markets.AccrualService, TypeID: 32, Amount: rub(-1000)},
		{Day: day, Kind: markets.AccrualService, TypeID: 41, Amount: rub(-500)},
	}
	d.Returns = []markets.OzonReturn{{Posting: "r-1", SKU: "22", Name: "Худи тестовое", Status: "WriteOff", Changed: day, Price: 5000}}
	loads, moneys := 0, 0
	tools := markets.ShopTools{
		Load:  func(context.Context, string) (markets.ShopData, error) { loads++; return shopData(), nil },
		Money: func(context.Context, string) (markets.ShopData, error) { moneys++; return d, nil },
	}
	money, err := tools.Run(context.Background(), "shop_money", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Тест", "продажи и возвраты 10 000 ₽", "вознаграждение Ozon -4 000 ₽ (40 %)", "доставка -1 000 ₽", "продвижение -500 ₽", "на счет 4 500 ₽", "до себестоимости"} {
		if !strings.Contains(money, want) {
			t.Errorf("money misses %q:\n%s", want, money)
		}
	}
	losses, err := tools.Run(context.Background(), "shop_losses", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"возможные пропажи: 1 на 5 000 ₽", "возврат списан", "Худи тестовое", "r-1", "компенсац"} {
		if !strings.Contains(losses, want) {
			t.Errorf("losses misses %q:\n%s", want, losses)
		}
	}
	if loads != 0 || moneys != 2 {
		t.Errorf("loads %d, moneys %d: деньги читаются своей загрузкой", loads, moneys)
	}
	// Без загрузки денег — ответ модели словами, а не паника.
	out, err := markets.ShopTools{Load: tools.Load}.Run(context.Background(), "shop_money", []byte(`{}`))
	if err != nil || !strings.Contains(out, "недоступн") {
		t.Errorf("no money loader: %q, %v", out, err)
	}
}

// Возвраты закрыты, начисления есть: деньги отвечают, пропажи называют причину (ревью #597).
func TestShopTools_returnsUnavailable(t *testing.T) {
	d := shopData()
	d.ReturnsErr = errors.New("нет доступа к возвратам")
	tools := markets.ShopTools{Money: func(context.Context, string) (markets.ShopData, error) { return d, nil }}
	if out, err := tools.Run(context.Background(), "shop_money", []byte(`{}`)); err != nil || !strings.Contains(out, "на счет") {
		t.Errorf("money without returns: %q, %v", out, err)
	}
	if out, err := tools.Run(context.Background(), "shop_losses", []byte(`{}`)); err != nil || !strings.Contains(out, "нет доступа к возвратам") {
		t.Errorf("losses without returns: %q, %v", out, err)
	}
}
