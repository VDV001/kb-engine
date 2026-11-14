package markets_test

import (
	"math"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Конкуренты ниши (#613): топ по выручке за период и что изменилось к
// прошлому такому же периоду — продажи, средняя цена, товаров в нише.
func TestCompetitors(t *testing.T) {
	cur := []markets.SellerStat{
		{ID: 1, Name: "Продавец А", Items: 120, Sales: 1500, Revenue: 1_500_000, AvgPrice: 1000},
		{ID: 2, Name: "Продавец Б", Items: 40, Sales: 900, Revenue: 2_700_000, AvgPrice: 3000},
		{ID: 3, Name: "Новичок", Items: 10, Sales: 100, Revenue: 50_000, AvgPrice: 500},
		{ID: 4, Name: "Без продаж", Items: 5},
	}
	prev := []markets.SellerStat{
		{ID: 1, Name: "Продавец А", Items: 100, Sales: 1000, Revenue: 1_100_000, AvgPrice: 1100},
		{ID: 2, Name: "Продавец Б", Items: 40, Sales: 1200, Revenue: 3_000_000, AvgPrice: 2500},
	}
	got := markets.Competitors(cur, prev, 3)
	if len(got) != 3 || got[0].Name != "Продавец Б" || got[1].Name != "Продавец А" || got[2].Name != "Новичок" {
		t.Fatalf("rows %+v, want top 3 by revenue: Б, А, Новичок", got)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	b, a, n := got[0], got[1], got[2]
	if !near(b.SalesChange, -0.25) || !near(b.PriceChange, 0.2) || b.ItemsAdded != 0 || b.New {
		t.Errorf("Б %+v: want sales −25 %%, price +20 %%, no new items", b)
	}
	if !near(a.SalesChange, 0.5) || !near(a.PriceChange, -1.0/11) || a.ItemsAdded != 20 {
		t.Errorf("А %+v: want sales +50 %%, price −9 %%, +20 items", a)
	}
	if !n.New || n.SalesChange != 0 || n.ItemsAdded != 0 {
		t.Errorf("Новичок %+v: new in the niche, no changes to a period it was not in", n)
	}
	if b.Share <= a.Share || !near(b.Share+a.Share+n.Share, (2_700_000+1_500_000+50_000)/4_250_000.0) {
		t.Errorf("shares %v %v %v of the niche revenue", b.Share, a.Share, n.Share)
	}
	if rows := markets.Competitors(nil, nil, 5); rows == nil || len(rows) != 0 {
		t.Errorf("empty niche: %v, want an empty list, not nil (JSON [])", rows)
	}
}

// Ревью #619: цена без продаж — не «−100 %», рост с нуля виден, продавцы без
// id не склеиваются.
func TestCompetitorsEdges(t *testing.T) {
	cur := []markets.SellerStat{
		{ID: 1, Name: "Ожил", Sales: 1500, Revenue: 1_500_000, AvgPrice: 1000},
		{ID: 2, Name: "Затих", Sales: 0, Revenue: 0, AvgPrice: 0, Items: 30},
		{Name: "Без id А", Sales: 10, Revenue: 10_000, AvgPrice: 1000},
		{Name: "Без id Б", Sales: 20, Revenue: 20_000, AvgPrice: 1000},
	}
	prev := []markets.SellerStat{
		{ID: 1, Name: "Ожил", Sales: 0, AvgPrice: 0},
		{ID: 2, Name: "Затих", Sales: 500, Revenue: 500_000, AvgPrice: 1000, Items: 30},
		{Name: "Без id Б", Sales: 10, Revenue: 10_000, AvgPrice: 1000},
	}
	rows := markets.Competitors(cur, prev, 10)
	by := map[string]markets.CompetitorRow{}
	for _, r := range rows {
		by[r.Name] = r
	}
	if r := by["Ожил"]; !r.New {
		t.Errorf("Ожил %+v: from no sales to many — marked like a newcomer, not flat", r)
	}
	if r := by["Затих"]; r.PriceChange != 0 || r.SalesChange != -1 {
		t.Errorf("Затих %+v: sales −100 %%, the price is unknown — no change, not −100 %%", r)
	}
	if r := by["Без id А"]; !r.New {
		t.Errorf("Без id А %+v: not in the previous period by name", r)
	}
	if r := by["Без id Б"]; r.New || r.SalesChange != 1 {
		t.Errorf("Без id Б %+v: matched by name, sales +100 %%", r)
	}
}
