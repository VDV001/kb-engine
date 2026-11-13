package domain

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// ErrForecastInput is returned when the sales series cannot give a forecast:
// нет нужных дней, нет продаж, день дважды, продажи меньше нуля.
var ErrForecastInput = errors.New("для прогноза не хватает данных")

// DaySales — продажи за день.
type DaySales struct {
	Day   time.Time
	Value float64
}

// DailySales — продажи по дням, по возрастанию даты, день один раз.
type DailySales struct{ days []DaySales }

// NewDailySales checks daily sales and sorts them; время дня отбрасывается.
func NewDailySales(ds []DaySales) (DailySales, error) {
	out := make([]DaySales, 0, len(ds))
	seen := map[time.Time]bool{}
	for _, d := range ds {
		if d.Day.IsZero() {
			return DailySales{}, fmt.Errorf("%w: день без даты", ErrForecastInput)
		}
		if d.Value < 0 || math.IsNaN(d.Value) || math.IsInf(d.Value, 0) {
			return DailySales{}, fmt.Errorf("%w: продажи за %s меньше нуля или не число", ErrForecastInput, d.Day.Format("02.01.2006"))
		}
		day := DayOf(d.Day)
		if seen[day] {
			return DailySales{}, fmt.Errorf("%w: %s дважды", ErrForecastInput, day.Format("02.01.2006"))
		}
		seen[day] = true
		out = append(out, DaySales{Day: day, Value: d.Value})
	}
	slices.SortFunc(out, func(a, b DaySales) int { return a.Day.Compare(b.Day) })
	return DailySales{days: out}, nil
}

// Days returns the days in date order.
func (s DailySales) Days() []DaySales { return slices.Clone(s.days) }

func (s DailySales) byDay() map[time.Time]float64 {
	m := make(map[time.Time]float64, len(s.days))
	for _, d := range s.days {
		m[d.Day] = d.Value
	}
	return m
}

// ForecastDay — прогноз на день и диапазон Lo..Hi.
type ForecastDay struct {
	Day           time.Time
	Value, Lo, Hi float64
}

// Forecast — прогноз своих продаж по дням (#580, макет «Прогноз»): форма
// сезона прошлого года × продажи ниши сейчас × своя доля. Диапазон — разброс
// прошлого года вокруг скользящего среднего за неделю.
type Forecast struct {
	days     []ForecastDay
	lastYear []DaySales
	growth   float64
	share    float64
}

// smoothDays — окно сглаживания прошлого года для разброса: неделя.
const smoothDays = 7

// NewForecast forecasts own sales. Окна берутся из данных: «сейчас» — дни
// nicheNow; у прошлого года должны быть те же дни годом раньше (база формы
// сезона) и дни после них — они и прогнозируются, годом позже.
func NewForecast(lastYear, nicheNow, own DailySales) (Forecast, error) {
	if len(nicheNow.days) == 0 {
		return Forecast{}, fmt.Errorf("%w: нет продаж ниши сейчас", ErrForecastInput)
	}
	nowMean := mean(nicheNow.days)
	if nowMean <= 0 {
		return Forecast{}, fmt.Errorf("%w: у ниши сейчас нет продаж", ErrForecastInput)
	}
	baseMean, err := lastYearBase(lastYear, nicheNow)
	if err != nil {
		return Forecast{}, err
	}
	share, err := ownShare(nicheNow, own)
	if err != nil {
		return Forecast{}, err
	}
	baseEnd := nicheNow.days[len(nicheNow.days)-1].Day.AddDate(-1, 0, 0)
	var target []DaySales
	for _, d := range lastYear.days {
		if d.Day.After(baseEnd) {
			target = append(target, d)
		}
	}
	if len(target) == 0 {
		return Forecast{}, fmt.Errorf("%w: в прошлом году нет дней после %s — нечего прогнозировать", ErrForecastInput, baseEnd.Format("02.01.2006"))
	}
	spread := spreadAroundWeek(target)
	f := Forecast{growth: nowMean/baseMean - 1, share: share}
	for _, d := range target {
		v := nowMean * d.Value / baseMean * share
		day := d.Day.AddDate(1, 0, 0)
		f.days = append(f.days, ForecastDay{Day: day, Value: v, Lo: math.Max(0, v*(1-spread)), Hi: v * (1 + spread)})
		f.lastYear = append(f.lastYear, DaySales{Day: day, Value: d.Value * share})
	}
	return f, nil
}

// lastYearBase is last year's mean on the days of nicheNow a year earlier.
func lastYearBase(lastYear, nicheNow DailySales) (float64, error) {
	ly := lastYear.byDay()
	var base []DaySales
	for _, d := range nicheNow.days {
		if v, ok := ly[d.Day.AddDate(-1, 0, 0)]; ok {
			base = append(base, DaySales{Day: d.Day, Value: v})
		}
	}
	if len(base) == 0 {
		first, last := nicheNow.days[0].Day.AddDate(-1, 0, 0), nicheNow.days[len(nicheNow.days)-1].Day.AddDate(-1, 0, 0)
		return 0, fmt.Errorf("%w: в прошлом году нет дней %s-%s", ErrForecastInput, first.Format("02.01.2006"), last.Format("02.01.2006"))
	}
	m := mean(base)
	if m <= 0 {
		return 0, fmt.Errorf("%w: у ниши в прошлом году в эти дни нет продаж", ErrForecastInput)
	}
	return m, nil
}

// ownShare is own sales over niche sales on the days both have.
func ownShare(nicheNow, own DailySales) (float64, error) {
	if len(own.days) == 0 {
		return 0, fmt.Errorf("%w: нет своих продаж", ErrForecastInput)
	}
	n := nicheNow.byDay()
	sumOwn, sumNiche := 0.0, 0.0
	for _, d := range own.days {
		if v, ok := n[d.Day]; ok {
			sumOwn += d.Value
			sumNiche += v
		}
	}
	if sumNiche <= 0 {
		return 0, fmt.Errorf("%w: свои продажи не за те дни, что продажи ниши", ErrForecastInput)
	}
	return sumOwn / sumNiche, nil
}

func mean(ds []DaySales) float64 {
	s := 0.0
	for _, d := range ds {
		s += d.Value
	}
	return s / float64(len(ds))
}

// spreadAroundWeek is the root mean square of the relative deviation from
// the centered weekly average: насколько прошлый год прыгал вокруг гладкой кривой.
func spreadAroundWeek(ds []DaySales) float64 {
	sum, n := 0.0, 0
	for i := range ds {
		lo, hi := max(0, i-smoothDays/2), min(len(ds), i+smoothDays/2+1)
		if ma := mean(ds[lo:hi]); ma > 0 {
			r := ds[i].Value/ma - 1
			sum += r * r
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// Days returns the forecast days.
func (f Forecast) Days() []ForecastDay { return slices.Clone(f.days) }

// LastYear returns last year's niche curve in own scale on the forecast days:
// так выглядел бы прошлый год при Вашей доле, без роста ниши.
func (f Forecast) LastYear() []DaySales { return slices.Clone(f.lastYear) }

// Growth is niche growth to last year on the same days.
func (f Forecast) Growth() float64 { return f.growth }

// Share is own share of the niche, по штукам.
func (f Forecast) Share() float64 { return f.share }

// Peak is the first day with the highest forecast.
func (f Forecast) Peak() ForecastDay {
	var p ForecastDay
	for _, d := range f.days {
		if d.Value > p.Value {
			p = d
		}
	}
	return p
}

// MonthTotal sums a month of the forecast with its range.
func (f Forecast) MonthTotal(year int, m time.Month) (v, lo, hi float64) {
	for _, d := range f.days {
		if d.Day.Year() == year && d.Day.Month() == m {
			v, lo, hi = v+d.Value, lo+d.Lo, hi+d.Hi
		}
	}
	return v, lo, hi
}
