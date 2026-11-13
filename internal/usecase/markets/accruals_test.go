package markets_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestOzonEconomy_groups(t *testing.T) {
	// Статьи разложены так же, как в «Экономике магазина» кабинета: раскладка
	// сверена до рубля на двух месяцах настоящего кабинета (суммы здесь выдуманы).
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rub := func(r int64) domain.Money { return domain.NewMoney(r * 100) }
	svc := func(id int, r int64) markets.OzonAccrual {
		return markets.OzonAccrual{Day: day, SKU: "11", Kind: markets.AccrualService, TypeID: id, Amount: rub(r)}
	}
	tests := []struct {
		name string
		acc  markets.OzonAccrual
		want func(markets.ShopEconomy) domain.Money
	}{
		{"продажа", markets.OzonAccrual{Day: day, SKU: "11", Kind: markets.AccrualSale, Amount: rub(1000)}, func(e markets.ShopEconomy) domain.Money { return e.Sales }},
		{"возврат — тоже в продажах", markets.OzonAccrual{Day: day, SKU: "11", Kind: markets.AccrualSale, Amount: rub(-1000)}, func(e markets.ShopEconomy) domain.Money { return e.Sales }},
		{"вознаграждение", markets.OzonAccrual{Day: day, SKU: "11", Kind: markets.AccrualCommission, Amount: rub(-480)}, func(e markets.ShopEconomy) domain.Money { return e.Commission }},
		{"логистика", svc(32, -80), func(e markets.ShopEconomy) domain.Money { return e.Delivery }},
		{"обратная логистика", svc(59, -70), func(e markets.ShopEconomy) domain.Money { return e.Delivery }},
		{"доставка до места выдачи силами Ozon", svc(98, -25), func(e markets.ShopEconomy) domain.Money { return e.Delivery }},
		{"оплата за клик", svc(41, -60), func(e markets.ShopEconomy) domain.Money { return e.Promotion }},
		{"продвижение товара", svc(54, -30), func(e markets.ShopEconomy) domain.Money { return e.Promotion }},
		{"доставка до места выдачи партнерами", svc(29, -12), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"эквайринг", svc(1, -9), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"обработка возвратов партнерами", svc(45, -15), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"упаковка партнерами", svc(39, -10), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"временное размещение партнерами", svc(79, -4), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"звездные товары", svc(74, -3), func(e markets.ShopEconomy) domain.Money { return e.Partners }},
		{"перенос карточек — в остальном", svc(24, -500), func(e markets.ShopEconomy) domain.Money { return e.Other }},
		{"товарная компенсация — в остальном", svc(25, 700), func(e markets.ShopEconomy) domain.Money { return e.Other }},
		{"незнакомая статья — в остальном, не теряется", svc(9999, -1), func(e markets.ShopEconomy) domain.Money { return e.Other }},
		{"разница итога и частей — в остальном", markets.OzonAccrual{Day: day, Kind: markets.AccrualRest, Amount: rub(2)}, func(e markets.ShopEconomy) domain.Money { return e.Other }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := markets.OzonEconomy([]markets.OzonAccrual{tt.acc})
			if got := tt.want(e); got != tt.acc.Amount {
				t.Errorf("line = %v, want %v (economy %+v)", got, tt.acc.Amount, e)
			}
			if e.Total != tt.acc.Amount {
				t.Errorf("total = %v, want %v", e.Total, tt.acc.Amount)
			}
		})
	}
}

func TestOzonEconomy_totalIsSumOfLines(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	e := markets.OzonEconomy([]markets.OzonAccrual{
		{Day: day, SKU: "11", Kind: markets.AccrualSale, Amount: domain.NewMoney(120000)},
		{Day: day, SKU: "11", Kind: markets.AccrualCommission, Amount: domain.NewMoney(-57600)},
		{Day: day, SKU: "11", Kind: markets.AccrualService, TypeID: 32, Amount: domain.NewMoney(-15000)},
		{Day: day, SKU: "11", Kind: markets.AccrualService, TypeID: 29, Amount: domain.NewMoney(-1150)},
	})
	// 1 200,00 − 576,00 − 150,00 − 11,50 = 462,50: копейки складываются точно.
	if e.Total != domain.NewMoney(46250) || e.Sales != domain.NewMoney(120000) || e.Delivery != domain.NewMoney(-15000) || e.Partners != domain.NewMoney(-1150) {
		t.Errorf("economy = %+v", e)
	}
}
