package markets_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestOzonSKUEconomy(t *testing.T) {
	// Выдуманный кабинет: брюки — две продажи и один возврат, у худи нет
	// себестоимости, реклама без SKU в товары не попадает.
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rub := func(r int64) domain.Money { return domain.NewMoney(r * 100) }
	line := func(sku string, kind markets.AccrualKind, typeID, qty int, r int64) markets.OzonAccrual {
		return markets.OzonAccrual{Day: day, SKU: sku, Unit: "p", Kind: kind, TypeID: typeID, Qty: qty, Amount: rub(r)}
	}
	acc := []markets.OzonAccrual{
		line("11", markets.AccrualSale, 0, 1, 2000), line("11", markets.AccrualCommission, 0, 1, -900), line("11", markets.AccrualService, 32, 1, -150),
		line("11", markets.AccrualSale, 0, 1, 2000), line("11", markets.AccrualCommission, 0, 1, -900), line("11", markets.AccrualService, 32, 1, -150),
		line("11", markets.AccrualSale, 0, 1, -2000), line("11", markets.AccrualCommission, 0, 1, 900), line("11", markets.AccrualService, 59, 1, -100),
		line("22", markets.AccrualSale, 0, 1, 5000), line("22", markets.AccrualCommission, 0, 1, -2500), line("22", markets.AccrualService, 1, 1, -50),
		{Day: day, Kind: markets.AccrualService, TypeID: 41, Amount: rub(-700)}, // реклама магазина — без SKU
	}
	costs := map[string]domain.Money{"11": rub(400)}
	got := markets.OzonSKUEconomy(acc, func(sku string) (domain.Money, bool) { m, ok := costs[sku]; return m, ok })
	if len(got) != 2 {
		t.Fatalf("economy = %+v, want 2 SKUs (реклама без SKU — не товар)", got)
	}
	// Худи: на счет 2 450 — больше брюк (на счет 700), идет первым.
	h, b := got[0], got[1]
	if h.SKU != "22" || h.Units != 1 || h.Total != rub(2450) || h.Partners != rub(-50) || h.HasCost || h.Profit() != rub(2450) {
		t.Errorf("hoodie = %+v, profit %v", h, h.Profit())
	}
	// Брюки: 2 − 1 = 1 шт, на счет 2000−900−150 ×2 −2000+900−100 = 700, себестоимость 400, прибыль 300.
	if b.SKU != "11" || b.Units != 1 || b.Sales != rub(2000) || b.Commission != rub(-900) || b.Delivery != rub(-400) || b.Total != rub(700) ||
		!b.HasCost || b.Cost() != rub(400) || b.Profit() != rub(300) {
		t.Errorf("trousers = %+v, cost %v, profit %v", b, b.Cost(), b.Profit())
	}
}

func TestReadCosts(t *testing.T) {
	rows := [][]string{
		{"Себестоимость FURIA, ноябрь"},
		{"Артикул продавца", "Название", "Себестоимость, ₽"},
		{"100_46", "Брюки", "1 250,50"},
		{" 200_L ", "Футболка", "480"},
		{"300_M", "Пижама", "1250.4999999"}, // число из Excel — до копейки
		{"", "", ""},
		{"400_S", "Худи", "дорого"},
		{"500_XL", "Куртка", ""},
	}
	got, probs, err := markets.ReadCosts(rows)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.Money{"100_46": domain.NewMoney(125050), "200_L": domain.NewMoney(48000), "300_M": domain.NewMoney(125050)}
	if len(got) != len(want) {
		t.Fatalf("costs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("cost %s = %v, want %v", k, got[k], v)
		}
	}
	if len(probs) != 2 || probs[0].Row != 7 || probs[1].Row != 8 {
		t.Errorf("problems = %+v, want rows 7 (не число) and 8 (пусто)", probs)
	}
	if _, _, err := markets.ReadCosts([][]string{{"SKU", "Цена"}, {"1", "2"}}); !errors.Is(err, markets.ErrNoCostColumns) {
		t.Errorf("table without артикул/себестоимость: err = %v", err)
	}
}
