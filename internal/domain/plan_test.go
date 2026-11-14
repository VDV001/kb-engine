package domain_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func knownPlanForecast(t *testing.T) domain.Forecast {
	t.Helper()
	last, now, own := knownForecast(t)
	f, err := domain.NewForecast(last, now, own)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Известный ответ (прогноз: ноябрь-декабрь 2026 по 30 шт, 11.11 — 90):
// остаток 1 000, цена 2 000, себестоимость 500, комиссия 40 %, логистика 10 %.
// Штука дает 2 000 × 0,5 − 500 = 500 ₽, на неделе пика (9-15.11) цена +10 % —
// 2 200 и 600 ₽. До 1.12 продано 990, 2.12 остаток кончается: 1 000 шт, из них
// 270 на неделе пика. Прибыль 270 × 600 + 730 × 500 = 527 000. Довезти
// 1 890 − 1 000 = 890 шт к 22.11 — за 10 дней до 2.12.
func TestNewPlan_knownAnswer(t *testing.T) {
	p, err := domain.NewPlan(knownPlanForecast(t), domain.PlanInput{Stock: 1000, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	if !near(p.Sell(), 1000) || !near(p.Profit(), 527000) {
		t.Errorf("sell %v, profit %v; want 1000 and 527000", p.Sell(), p.Profit())
	}
	if qty, by := p.Supply(); !near(qty, 890) || !by.Equal(day(2026, 11, 22)) {
		t.Errorf("supply %v by %v; want 890 by 22.11.2026", qty, by)
	}
	if p.ShortForPeak() {
		t.Error("stock lasts past the peak, short for peak = true")
	}
	if n := len(p.Days()); n != 61 {
		t.Errorf("days = %d, want 61 (ноябрь и декабрь)", n)
	}
	var peak, short *domain.PlanWeek
	ws := p.Weeks()
	for i := range ws {
		w := &ws[i]
		if w.Peak {
			peak = w
		}
		if w.Short && short == nil {
			short = w
		}
		if w.Dip {
			t.Errorf("week %v marked as a dip on a flat forecast", w.From)
		}
	}
	if peak == nil || !peak.From.Equal(day(2026, 11, 9)) || !near(peak.Price, 2200) || !near(peak.Sell, 270) || !near(peak.Profit, 162000) {
		t.Errorf("peak week = %+v", peak)
	}
	if short == nil || !short.From.Equal(day(2026, 11, 30)) || !near(short.Sell, 70) || !near(short.StockEnd, 0) {
		t.Errorf("first short week = %+v; want 30.11 with 70 sold (30 + 30 + 10)", short)
	}
	if len(ws) == 0 {
		t.Fatal("no weeks")
	}
	if ws[0].Days != 1 || !ws[0].From.Equal(day(2026, 10, 26)) {
		t.Errorf("first week = %+v; want the partial week of 01.11 (from Monday 26.10)", ws[0])
	}
}

// Остатка не хватает до пика: предупреждение и поставка с запасом.
func TestNewPlan_shortForPeak(t *testing.T) {
	p, err := domain.NewPlan(knownPlanForecast(t), domain.PlanInput{Stock: 200, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	// 1-6.11 продано 180, 7.11 — 20 из 30: кончается 7.11, раньше пика 11.11.
	if !p.ShortForPeak() {
		t.Error("stock ends 07.11, before the peak 11.11")
	}
	if qty, by := p.Supply(); qty != 1690 || !by.Equal(day(2026, 10, 28)) {
		t.Errorf("supply %v by %v; want 1690 by 28.10.2026", qty, by)
	}
	// Для одного предупреждения словами: когда кончится остаток и весь спрос.
	if !p.OutOfStock().Equal(day(2026, 11, 7)) || p.Demand() != 1890 {
		t.Errorf("out %v, demand %v; want 07.11.2026 and 1890", p.OutOfStock(), p.Demand())
	}
}

// Провал — неделя, где спрос в день меньше 80 % среднего: скидка 10 %.
func TestNewPlan_dip(t *testing.T) {
	_, now, own := knownForecast(t)
	last := series(t, day(2025, 9, 1), day(2025, 12, 31), func(d time.Time) float64 {
		switch {
		case d.Month() == time.December && d.Day() >= 14 && d.Day() <= 20:
			return 100 // половина спроса — неделя 14-20.12.2026 (пн 14.12)
		case d.Month() >= time.November:
			return 200
		}
		return 100
	})
	f, err := domain.NewForecast(last, now, own)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.NewPlan(f, domain.PlanInput{Stock: 5000, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range p.Weeks() {
		if w.From.Equal(day(2026, 12, 14)) {
			if !w.Dip || w.Price != 1800 {
				t.Errorf("dip week = %+v; want dip at 1800", w)
			}
			return
		}
	}
	t.Error("no week from 14.12.2026")
}

func TestNewPlan_refuses(t *testing.T) {
	f := knownPlanForecast(t)
	ok := domain.PlanInput{Stock: 10, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10}
	tests := []struct {
		name string
		in   func(domain.PlanInput) domain.PlanInput
	}{
		{"цена ноль", func(in domain.PlanInput) domain.PlanInput { in.Price = 0; return in }},
		{"себестоимость меньше нуля", func(in domain.PlanInput) domain.PlanInput { in.Cost = -1; return in }},
		{"остаток меньше нуля", func(in domain.PlanInput) domain.PlanInput { in.Stock = -1; return in }},
		{"комиссия и логистика съедают всю цену", func(in domain.PlanInput) domain.PlanInput { in.Commission, in.Logistics = 0.7, 0.3; return in }},
		{"доля меньше нуля", func(in domain.PlanInput) domain.PlanInput { in.Logistics = -0.1; return in }},
		{"срок поставки меньше нуля", func(in domain.PlanInput) domain.PlanInput { in.LeadDays = -1; return in }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := domain.NewPlan(f, tt.in(ok)); !errors.Is(err, domain.ErrInvalidPlan) {
				t.Errorf("err = %v, want ErrInvalidPlan", err)
			}
		})
	}
	if _, err := domain.NewPlan(domain.Forecast{}, ok); !errors.Is(err, domain.ErrInvalidPlan) {
		t.Errorf("empty forecast: err = %v", err)
	}
}

// Ревью #599: план — с сегодня, срок поставки в прошлом назван, нулевой прогноз — отказ.
func TestNewPlan_fromToday(t *testing.T) {
	f := knownPlanForecast(t)
	// Сегодня 13.11: 1-12.11 уже прошли (12 дней × 30 + 60 сверху за 11.11 = 420 шт
	// спроса не в плане). Остаток 1 000 на сегодня: 13-30.11 — 540, 1-15.12 — 450,
	// итого 990 к 15.12, 16.12 кончается. Довезти 1 890 − 420 − 1 000 = 470 к 06.12.
	p, err := domain.NewPlan(f, domain.PlanInput{Stock: 1000, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10, From: day(2026, 11, 13)})
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Days(); len(d) != 49 || !d[0].Day.Equal(day(2026, 11, 13)) {
		t.Errorf("days = %d from %v; want 49 from 13.11", len(d), d[0].Day)
	}
	if qty, by := p.Supply(); math.Abs(qty-470) > 1e-6 || !by.Equal(day(2026, 12, 6)) || p.SupplyLate() {
		t.Errorf("supply %v by %v late %v; want 470 by 06.12, not late", qty, by, p.SupplyLate())
	}
	// Остаток 200 на 01.11 — кончится 07.11, везти надо было к 28.10: срок прошел.
	late, err := domain.NewPlan(f, domain.PlanInput{Stock: 200, Cost: 500, Price: 2000, Commission: 0.4, Logistics: 0.1, LeadDays: 10, From: day(2026, 11, 1)})
	if err != nil || !late.SupplyLate() {
		t.Errorf("supply by 28.10 when today is 01.11: late = %v, err %v", late.SupplyLate(), err)
	}
	// Сегодня после конца прогноза — плана нет.
	if _, err := domain.NewPlan(f, domain.PlanInput{Stock: 1, Cost: 1, Price: 100, From: day(2027, 1, 5)}); !errors.Is(err, domain.ErrInvalidPlan) {
		t.Errorf("today after the forecast: err = %v", err)
	}
}

func TestNewPlan_zeroForecast(t *testing.T) {
	last, now, _ := knownForecast(t)
	own := series(t, day(2026, 9, 1), day(2026, 10, 31), func(time.Time) float64 { return 0 })
	f, err := domain.NewForecast(last, now, own)
	if err != nil {
		t.Skipf("forecast refuses zero own sales itself: %v", err)
	}
	if _, err := domain.NewPlan(f, domain.PlanInput{Stock: 1, Cost: 1, Price: 100}); !errors.Is(err, domain.ErrInvalidPlan) {
		t.Errorf("zero forecast: err = %v, want ErrInvalidPlan (пика нет)", err)
	}
}
