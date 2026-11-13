package markets_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Магазин» за выбранные даты (отзыв владельца 13.11): отправления и начисления
// из загруженных 90 дней оставляются по дню, оба края включительно.
func TestShopPeriod(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	from, to := time.Date(2026, 11, 1, 0, 0, 0, 0, msk), time.Date(2026, 11, 7, 0, 0, 0, 0, msk)
	posts := []markets.OzonPosting{
		{Number: "до", At: time.Date(2026, 10, 31, 23, 59, 0, 0, msk)},
		{Number: "первый день", At: from},
		{Number: "последний день", At: time.Date(2026, 11, 7, 23, 59, 0, 0, msk)},
		{Number: "после", At: time.Date(2026, 11, 8, 0, 0, 0, 0, msk)},
		{Number: "без даты"},
	}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) } // начисление — день, полночь UTC
	acc := []markets.OzonAccrual{{Unit: "a", Day: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}, {Unit: "b", Day: day(1)}, {Unit: "c", Day: day(7)}, {Unit: "d", Day: day(8)}}

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"отправления", numbers(markets.PostingsIn(posts, from, to)), []string{"первый день", "последний день"}},
		{"начисления", units(markets.AccrualsIn(acc, from, to)), []string{"b", "c"}},
	}
	for _, tc := range tests {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func numbers(ps []markets.OzonPosting) (out []string) {
	for _, p := range ps {
		out = append(out, p.Number)
	}
	return out
}

func units(as []markets.OzonAccrual) (out []string) {
	for _, a := range as {
		out = append(out, a.Unit)
	}
	return out
}

// Ниши из MPStats (#580): продажи и выручка за период — в месяц, цена —
// выручка на штуку, рост — к тем же дням год назад; имя — полный путь.
func TestMPStatsNiches(t *testing.T) {
	cur := []markets.NicheSale{{Name: "Куртки", Sales: 300, Revenue: 3000000, Sellers: 40, Top10Revenue: 1500000}, {Name: "Новая", Sales: 10, Revenue: 1000, Sellers: 1}}
	prev := []markets.NicheSale{{Name: "Куртки", Sales: 200, Revenue: 1}}
	tb, err := markets.MPStatsNiches("Одежда/Мужская одежда", cur, prev, 30)
	if err != nil {
		t.Fatal(err)
	}
	if tb.PeriodDays != 30 || len(tb.Niches) != 2 || tb.Niches[0].Name() != "Одежда/Мужская одежда/Куртки" {
		t.Fatalf("table = %+v", tb)
	}
	n := tb.Niches[0]
	for _, c := range []struct {
		col  domain.NicheColumn
		want float64
	}{{domain.NicheSales, 300 / 30.0 * 365.25 / 12}, {domain.NicheSellers, 40}, {domain.NichePrice, 10000}, {domain.NicheGrowth, 0.5}, {domain.NicheTop10, 0.5}} {
		if v, ok := n.Value(c.col); !ok || math.Abs(v-c.want) > 1e-6 {
			t.Errorf("%s = %v (%v), want %v", c.col, v, ok, c.want)
		}
	}
	if _, ok := tb.Niches[1].Value(domain.NicheGrowth); ok {
		t.Error("a niche without last year's sales got a growth")
	}
	if _, ok := tb.Niches[1].Value(domain.NicheTop10); ok {
		t.Error("a niche without sellers' revenue got a top-10 share")
	}
}
