package domain

import (
	"cmp"
	"slices"
)

// ShopAdviceKind — что посоветовать магазину (#580, «Магазин → Советы»).
// Слова совета пишет окно; домен решает, кому и по какому правилу.
type ShopAdviceKind string

// Советы, в порядке важности.
const (
	AdviceRefusals  ShopAdviceKind = "refusals" // отказываются при вручении: посадка, замеры, фото
	AdviceSupply    ShopAdviceKind = "supply"   // кончается ходовой товар: довезти
	AdviceDeadStock ShopAdviceKind = "dead"     // остаток без единого заказа за период
	AdviceKeep      ShopAdviceKind = "keep"     // продается чисто: держать в наличии
)

// ShopAdvice — совет и товары, к которым он относится: SKU в порядке
// важности, Units — штук в совете (отказов, довезти, лежит).
type ShopAdvice struct {
	Kind  ShopAdviceKind
	SKUs  []string
	Units float64
}

// Пороги советов.
// ponytail: пороги из одного кабинета одежды на Ozon (#580); потолок —
// другая категория; путь — пороги в настройках модуля, как у проблем.
const (
	adviceMinOrders   = 10  // меньше заказов — доли случайны
	adviceRefusals    = 0.4 // доля отказов при вручении
	adviceKeepRefused = 0.2 // отказов не больше
	adviceKeepCancels = 0.3 // и отмен не больше
)

// AdviseShop gives the shop's advice; совета без товаров нет.
func AdviseShop(ps []*ShopProduct) []ShopAdvice {
	rules := []struct {
		kind  ShopAdviceKind
		fits  func(d ShopProductData, p *ShopProduct) bool
		units func(d ShopProductData) float64
		order func(d ShopProductData) float64
	}{
		{AdviceRefusals, func(d ShopProductData, p *ShopProduct) bool {
			return d.Ordered >= adviceMinOrders && p.RefusedRate() >= adviceRefusals
		}, func(d ShopProductData) float64 { return d.Refused }, func(d ShopProductData) float64 { return d.Refused }},
		{AdviceSupply, func(d ShopProductData, _ *ShopProduct) bool { return d.UrgentSupply },
			func(d ShopProductData) float64 { return d.SupplyQty }, func(d ShopProductData) float64 { return d.SupplyQty }},
		{AdviceDeadStock, func(d ShopProductData, _ *ShopProduct) bool { return d.Ordered == 0 && d.Stock > 0 },
			func(d ShopProductData) float64 { return d.Stock }, func(d ShopProductData) float64 { return d.Stock }},
		{AdviceKeep, func(d ShopProductData, p *ShopProduct) bool {
			return d.Ordered >= adviceMinOrders && p.RefusedRate() <= adviceKeepRefused && p.CancelRate() <= adviceKeepCancels
		}, func(ShopProductData) float64 { return 0 }, func(d ShopProductData) float64 { return d.Ordered }},
	}
	var out []ShopAdvice
	for _, r := range rules {
		var fit []*ShopProduct
		a := ShopAdvice{Kind: r.kind}
		for _, p := range ps {
			if d := p.Data(); r.fits(d, p) {
				fit = append(fit, p)
				a.Units += r.units(d)
			}
		}
		if len(fit) == 0 {
			continue
		}
		slices.SortStableFunc(fit, func(x, y *ShopProduct) int { return cmp.Compare(r.order(y.Data()), r.order(x.Data())) })
		for _, p := range fit {
			a.SKUs = append(a.SKUs, p.Data().SKU)
		}
		out = append(out, a)
	}
	return out
}
