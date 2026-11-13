package domain_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestRecognizeNicheMonth(t *testing.T) {
	for _, tc := range []struct {
		header string
		month  time.Month
		year   int
		ok     bool
	}{
		{"Ноябрь 2025", time.November, 2025, true},
		{"ноя 2025", time.November, 2025, true},
		{"Дек.25", time.December, 2025, true},
		{"Январь", time.January, 0, true},
		{"мая 2026", time.May, 2026, true},
		{"Продажи, окт 2026", time.October, 2026, true},
		{"2026-03", time.March, 2026, true},
		{"03.2026", time.March, 2026, true},
		{"Маркетплейс", 0, 0, false},
		{"Продажи, шт", 0, 0, false},
		{"2026-13", 0, 0, false},
		{"", 0, 0, false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			m, y, ok := domain.RecognizeNicheMonth(tc.header)
			if ok != tc.ok || m != tc.month || y != tc.year {
				t.Errorf("RecognizeNicheMonth(%q) = %v %d %v; want %v %d %v", tc.header, m, y, ok, tc.month, tc.year, tc.ok)
			}
		})
	}
	// Заголовок месяца — не величина: иначе «Продажи, окт 2026» стал бы продажами.
	if c, ok := domain.RecognizeNicheColumn("Продажи, окт 2026"); ok {
		t.Errorf("month header recognized as column %q", c)
	}
}

func TestParseMarketplaces(t *testing.T) {
	for _, tc := range []struct {
		cell string
		want []domain.Marketplace
		err  bool
	}{
		{"WB", []domain.Marketplace{domain.MarketWB}, false},
		{"Wildberries, Ozon", []domain.Marketplace{domain.MarketWB, domain.MarketOzon}, false},
		{"озон / вб", []domain.Marketplace{domain.MarketWB, domain.MarketOzon}, false},
		{"ozon; ozon", []domain.Marketplace{domain.MarketOzon}, false},
		{"", nil, false},
		{"Авито", nil, true},
	} {
		t.Run(tc.cell, func(t *testing.T) {
			got, err := domain.ParseMarketplaces(tc.cell)
			if tc.err {
				if !errors.Is(err, domain.ErrUnknownMarketplace) {
					t.Errorf("err = %v, want ErrUnknownMarketplace", err)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("ParseMarketplaces(%q) = %v, %v; want %v", tc.cell, got, err, tc.want)
			}
		})
	}
	if c, ok := domain.RecognizeNicheColumn("Маркетплейс"); !ok || c != domain.NicheMarketplace {
		t.Errorf("«Маркетплейс» = %q, %v", c, ok)
	}
	if _, err := domain.NewNiche("x", map[domain.NicheColumn]float64{domain.NicheMarketplace: 1}); !errors.Is(err, domain.ErrInvalidNiche) {
		t.Errorf("marketplace as a number: err = %v", err)
	}
}

// sales12 — продажи ноя 2025 — окт 2026 с пиком в декабре.
func sales12(peakAt int) []domain.MonthSales {
	out := make([]domain.MonthSales, 12)
	for i := range out {
		m := time.Month((10+i)%12 + 1) // ноябрь первым
		out[i] = domain.MonthSales{Month: m, Year: 2025 + (10+i)/12, Value: 10}
	}
	out[peakAt].Value = 100
	return out
}

func TestNiche_monthsAndPeak(t *testing.T) {
	n, _ := domain.NewNiche("Куртки", nil)
	withM, err := n.WithMonths(sales12(1))
	if err != nil {
		t.Fatalf("WithMonths: %v", err)
	}
	if p, ok := withM.PeakMonth(); !ok || p != time.December {
		t.Errorf("PeakMonth = %v, %v; want December", p, ok)
	}
	if _, ok := n.PeakMonth(); ok {
		t.Error("niche without months has a peak")
	}
	if few, _ := n.WithMonths(sales12(1)[:5]); few != nil {
		if _, ok := few.PeakMonth(); ok {
			t.Error("five months are too few for a peak")
		}
	}
	bad := sales12(1)
	bad[3].Value = -1
	if _, err := n.WithMonths(bad); !errors.Is(err, domain.ErrInvalidNiche) {
		t.Errorf("negative month: err = %v", err)
	}
	dup := sales12(1)
	dup[2] = dup[1]
	if _, err := n.WithMonths(dup); !errors.Is(err, domain.ErrInvalidNiche) {
		t.Errorf("month twice: err = %v", err)
	}
	mp, err := n.WithMarketplaces([]domain.Marketplace{domain.MarketOzon})
	if err != nil || !slices.Equal(mp.Marketplaces(), []domain.Marketplace{domain.MarketOzon}) {
		t.Errorf("WithMarketplaces = %v, %v", mp.Marketplaces(), err)
	}
	if len(n.Marketplaces()) != 0 || len(n.Months()) != 0 {
		t.Error("With… changed the original niche")
	}
}

func TestScoreNiches(t *testing.T) {
	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	mk := func(name string, vals map[domain.NicheColumn]float64, peakAt int) *domain.Niche {
		n, err := domain.NewNiche(name, vals)
		if err != nil {
			t.Fatal(err)
		}
		if peakAt >= 0 {
			if n, err = n.WithMonths(sales12(peakAt)); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	// Выручки нет — берется продажи × цена.
	jackets := mk("Куртки", map[domain.NicheColumn]float64{domain.NicheSales: 182000, domain.NichePrice: 7490, domain.NicheSellers: 1840, domain.NicheGrowth: 0.12, domain.NicheTop10: 0.38}, 1)
	swim := mk("Купальники", map[domain.NicheColumn]float64{domain.NicheRevenue: 328500000, domain.NicheSellers: 2600, domain.NicheGrowth: 0.09, domain.NicheTop10: 0.25}, 7)
	noMonths := mk("Без месяцев", map[domain.NicheColumn]float64{domain.NicheRevenue: 100, domain.NicheSellers: 1, domain.NicheGrowth: 0.5, domain.NicheTop10: 0.5}, -1)
	bare := mk("Только продажи", map[domain.NicheColumn]float64{domain.NicheSales: 5}, -1)

	got := domain.ScoreNiches([]*domain.Niche{jackets, swim, noMonths, bare}, now)
	if len(got) != 4 {
		t.Fatalf("scores = %d", len(got))
	}
	j := got[0]
	if !j.Ok || j.Score < 1 || j.Score > 100 {
		t.Fatalf("jackets score = %+v", j)
	}
	// Куртки: рост 0.12 → (0.12+0.1)/0.55 = 0.4; на продавца — максимум → 1; топ-10 0.38 → 0.62; пик в декабре через 2 месяца → 1.
	if want := 100 * (0.4*0.4 + 0.3*1 + 0.2*0.62 + 0.1*1); j.Score != int(want+0.5) {
		t.Errorf("jackets score = %d, want %d", j.Score, int(want+0.5))
	}
	if !j.InSeason || j.Peak != time.December || j.WeeksFrom != 6 || j.WeeksTo != 11 {
		t.Errorf("jackets season = in %v peak %v weeks %d-%d; want in December, 6-11 weeks", j.InSeason, j.Peak, j.WeeksFrom, j.WeeksTo)
	}
	if j.Growth != domain.GrowthModerate || j.Crowded || !j.HasPerSeller || int(j.PerSeller) != 182000*7490/1840 {
		t.Errorf("jackets facts = %+v", j)
	}
	s := got[1]
	if s.InSeason || s.Peak != time.June || !s.Ok || s.Score >= j.Score {
		t.Errorf("swimsuits = %+v; out of season, below jackets", s)
	}
	n := got[2]
	if !n.Ok || n.HasPeak || !slices.Contains(n.Missing, domain.NicheMissingMonths) || n.Growth != domain.GrowthFast || n.Crowded != true {
		t.Errorf("no months = %+v; score without the season part, months named missing", n)
	}
	b := got[3]
	if b.Ok || len(b.Missing) == 0 {
		t.Errorf("bare = %+v; no score, missing named", b)
	}
	if got := domain.ScoreNiches(nil, now); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
}
