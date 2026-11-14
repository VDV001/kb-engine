package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrInvalidPlan — ввод плана не годится: цена, себестоимость, доли или остаток.
var ErrInvalidPlan = errors.New("план не построить")

// PlanInput — ввод «Плана» (#580): остаток, себестоимость и цена за штуку,
// комиссия и логистика долей цены (0.4 — 40 %), за сколько дней везти поставку.
type PlanInput struct {
	Stock, Cost, Price    float64
	Commission, Logistics float64
	LeadDays              int
	// From — сегодня: остаток — на сегодня, прошедшие дни прогноза уже
	// проданы; ноль — с первого дня прогноза (ревью #599).
	From time.Time
}

// PlanDay — день плана: спрос прогноза, сколько продать, остаток на конец, цена.
type PlanDay struct {
	Day                           time.Time
	Demand, Sell, StockEnd, Price float64
	Profit                        float64
}

// PlanWeek — неделя плана с понедельника: суммы дней и пометки.
type PlanWeek struct {
	From                          time.Time
	Days                          int
	Demand, Sell, StockEnd, Price float64
	Profit                        float64
	Peak, Dip, Short              bool
}

// Plan — сколько продавать по дням и неделям и сколько довезти к какой дате.
type Plan struct {
	days          []PlanDay
	weeks         []PlanWeek
	demand, stock float64
	out, peak     time.Time // день, когда остатка не хватило (ноль — хватило), и день пика
	from          time.Time // первый день плана — сегодня
	lead          int
}

// Цены плана: на неделе пика выше, в провал — скидка.
// ponytail: шаги цены и порог провала — правило из макета #580, без
// эластичности: спрос от цены в плане не меняется. Потолок — товар, где
// скидка двигает спрос сильно; путь — эластичность из своих продаж по ценам.
const (
	peakMarkup   = 1.1 // неделя пика: +10 %
	dipDiscount  = 0.9 // провал: −10 %
	dipThreshold = 0.8 // провал — спрос в день ниже 80 % среднего
)

// NewPlan builds the plan from the forecast: продать не больше остатка, цена
// по неделе, прибыль штуки — цена × (1 − комиссия − логистика) − себестоимость;
// довезти недостающее к дню, когда остаток кончится, минус срок поставки.
func NewPlan(f Forecast, in PlanInput) (Plan, error) {
	if why := in.broken(); why != "" {
		return Plan{}, fmt.Errorf("%w: %s", ErrInvalidPlan, why)
	}
	if len(f.days) == 0 {
		return Plan{}, fmt.Errorf("%w: прогноз пуст", ErrInvalidPlan)
	}
	if f.Peak().Day.IsZero() {
		return Plan{}, fmt.Errorf("%w: прогноз нулевой — продаж не ждем, пика нет", ErrInvalidPlan)
	}
	days := f.days
	if !in.From.IsZero() { // прошедшие дни уже проданы: остаток — на сегодня (ревью #599)
		days = slices.DeleteFunc(slices.Clone(days), func(d ForecastDay) bool { return d.Day.Before(in.From) })
	}
	if len(days) == 0 {
		return Plan{}, fmt.Errorf("%w: прогноз кончился до сегодня", ErrInvalidPlan)
	}
	prices := weekPrices(f, in.Price)
	p := Plan{lead: in.LeadDays, peak: f.Peak().Day, from: days[0].Day}
	left := in.Stock
	for _, d := range days {
		price := prices[monday(d.Day)]
		sell := min(d.Value, left)
		left -= sell
		p.days = append(p.days, PlanDay{Day: d.Day, Demand: d.Value, Sell: sell, StockEnd: left, Price: price,
			Profit: sell * (price*(1-in.Commission-in.Logistics) - in.Cost)})
		p.demand += d.Value
		if sell < d.Value && p.out.IsZero() {
			p.out = d.Day
		}
	}
	p.stock = in.Stock
	p.weeks = weeksOf(p.days, monday(p.peak), prices, in.Price)
	return p, nil
}

func (in PlanInput) broken() string {
	switch {
	case in.Price <= 0:
		return "цена должна быть больше нуля"
	case in.Cost < 0:
		return "себестоимость меньше нуля"
	case in.Stock < 0:
		return "остаток меньше нуля"
	case in.Commission < 0 || in.Logistics < 0:
		return "комиссия и логистика не бывают меньше нуля"
	case in.Commission+in.Logistics >= 1:
		return "комиссия и логистика съедают всю цену"
	case in.LeadDays < 0:
		return "срок поставки меньше нуля"
	}
	return ""
}

// monday is the Monday of t's week.
func monday(t time.Time) time.Time { return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)) }

// weekPrices sets each week's price: пик — выше, провал — скидка.
func weekPrices(f Forecast, price float64) map[time.Time]float64 {
	sum, n := map[time.Time]float64{}, map[time.Time]int{}
	total := 0.0
	for _, d := range f.days {
		w := monday(d.Day)
		sum[w] += d.Value
		n[w]++
		total += d.Value
	}
	avg := total / float64(len(f.days))
	peak := monday(f.Peak().Day)
	out := map[time.Time]float64{}
	for w, s := range sum {
		switch {
		case w.Equal(peak):
			out[w] = price * peakMarkup
		case s/float64(n[w]) < dipThreshold*avg:
			out[w] = price * dipDiscount
		default:
			out[w] = price
		}
	}
	return out
}

// weeksOf sums days into weeks from Monday with their marks.
func weeksOf(days []PlanDay, peak time.Time, prices map[time.Time]float64, base float64) []PlanWeek {
	var out []PlanWeek
	for _, d := range days {
		w := monday(d.Day)
		if len(out) == 0 || !out[len(out)-1].From.Equal(w) {
			out = append(out, PlanWeek{From: w, Price: prices[w], Peak: w.Equal(peak), Dip: prices[w] < base})
		}
		x := &out[len(out)-1]
		x.Days++
		x.Demand += d.Demand
		x.Sell += d.Sell
		x.Profit += d.Profit
		x.StockEnd = d.StockEnd
		x.Short = x.Short || d.Sell < d.Demand
	}
	return out
}

// Days returns the plan by day.
func (p Plan) Days() []PlanDay { return slices.Clone(p.days) }

// Weeks returns the plan by week.
func (p Plan) Weeks() []PlanWeek { return slices.Clone(p.weeks) }

// Sell is the plan's total sales, штук.
func (p Plan) Sell() float64 {
	s := 0.0
	for _, d := range p.days {
		s += d.Sell
	}
	return s
}

// Profit is the plan's profit.
func (p Plan) Profit() float64 {
	s := 0.0
	for _, d := range p.days {
		s += d.Profit
	}
	return s
}

// Supply — сколько довезти и к какой дате: недостающее к спросу, к дню, когда
// остаток кончится, минус срок поставки; ноль — остатка хватает.
func (p Plan) Supply() (qty float64, by time.Time) {
	if p.out.IsZero() {
		return 0, time.Time{}
	}
	return max(p.demand-p.stock, 0), p.out.AddDate(0, 0, -p.lead)
}

// Peak is the forecast's peak day.
func (p Plan) Peak() time.Time { return p.peak }

// SupplyLate — срок поставки уже прошел: везти сейчас.
func (p Plan) SupplyLate() bool {
	_, by := p.Supply()
	return !by.IsZero() && by.Before(p.from)
}

// OutOfStock is the day the stock ran short of demand; ноль — хватило.
func (p Plan) OutOfStock() time.Time { return p.out }

// Demand is the forecast's demand over the plan, штук.
func (p Plan) Demand() float64 { return p.demand }

// ShortForPeak — остаток кончится раньше дня пика или в него.
func (p Plan) ShortForPeak() bool { return !p.out.IsZero() && !p.out.After(p.peak) }
