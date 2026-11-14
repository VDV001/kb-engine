package markets

import (
	"cmp"
	"slices"
	"strconv"
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
// периоде у него в нише не было продаж (или его самого): изменений нет.
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
	before := make(map[string]SellerStat, len(prev))
	for _, s := range prev {
		before[sellerKey(s)] = s
	}
	top := slices.Clone(cur)
	slices.SortStableFunc(top, func(a, b SellerStat) int { return cmp.Compare(b.Revenue, a.Revenue) })
	out := make([]CompetitorRow, 0, min(n, len(top)))
	for _, s := range top[:min(n, len(top))] {
		r := CompetitorRow{SellerStat: s}
		if total > 0 {
			r.Share = s.Revenue / total
		}
		p, ok := before[sellerKey(s)]
		switch {
		case !ok || p.Sales <= 0: // рост с нуля — как новичок, а не «без изменений» (ревью #619)
			r.New = true
		default:
			r.SalesChange, r.PriceChange = change(p.Sales, s.Sales), priceChange(p.AvgPrice, s.AvgPrice)
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

// priceChange: без продаж средней цены нет — это не «−100 %» (ревью #619).
func priceChange(was, now float64) float64 {
	if now <= 0 {
		return 0
	}
	return change(was, now)
}

// sellerKey — id продавца, а без него — имя: продавцы без id не склеиваются.
func sellerKey(s SellerStat) string {
	if s.ID != 0 {
		return "id:" + strconv.FormatInt(s.ID, 10)
	}
	return "name:" + s.Name
}
