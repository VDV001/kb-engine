package markets

import (
	"cmp"
	"slices"
)

// SellerStat — продавец ниши за период из сервиса аналитики.
type SellerStat struct {
	ID               int64
	Name             string
	Items, ItemsSold int     // товаров в нише и с продажами
	Sales, Revenue   float64 // штук и рублей за период
	AvgPrice, Rating float64
	Comments         float64 // отзывов на товар в среднем
}

// CompetitorRow — продавец ниши и что у него изменилось к прошлому такому же
// периоду: доли (−0,25 — минус 25 %), товаров прибавилось. New — в прошлом
// периоде его в нише не было, изменений нет.
type CompetitorRow struct {
	SellerStat
	Share                    float64 // доля выручки ниши за период
	SalesChange, PriceChange float64
	ItemsAdded               int
	New                      bool
}

// Competitors is the top n sellers of the niche by revenue for the period
// cur with changes to prev (#613): рост и падение продаж, цена, новинки.
func Competitors(cur, prev []SellerStat, n int) []CompetitorRow {
	var total float64
	for _, s := range cur {
		total += s.Revenue
	}
	before := make(map[int64]SellerStat, len(prev))
	for _, s := range prev {
		before[s.ID] = s
	}
	top := slices.Clone(cur)
	slices.SortStableFunc(top, func(a, b SellerStat) int { return cmp.Compare(b.Revenue, a.Revenue) })
	out := make([]CompetitorRow, 0, min(n, len(top)))
	for _, s := range top[:min(n, len(top))] {
		r := CompetitorRow{SellerStat: s}
		if total > 0 {
			r.Share = s.Revenue / total
		}
		p, ok := before[s.ID]
		switch {
		case !ok:
			r.New = true
		default:
			r.SalesChange, r.PriceChange = change(p.Sales, s.Sales), change(p.AvgPrice, s.AvgPrice)
			r.ItemsAdded = max(s.Items-p.Items, 0)
		}
		out = append(out, r)
	}
	return out
}

// change is the share by which now differs from was; 0 — сравнить не с чем.
func change(was, now float64) float64 {
	if was <= 0 {
		return 0
	}
	return now/was - 1
}
