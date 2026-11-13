package domain_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// series — продажи по дням с from по to включительно; f дает продажи дня.
func series(t *testing.T, from, to time.Time, f func(time.Time) float64) domain.DailySales {
	t.Helper()
	var ds []domain.DaySales
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		ds = append(ds, domain.DaySales{Day: d, Value: f(d)})
	}
	s, err := domain.NewDailySales(ds)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Выдуманные данные с известным ответом:
// ниша прошлого года — сен-окт 2025 по 100 шт в день, ноя-дек 2025 по 200,
// 11.11.2025 — 600 (распродажа); ниша сейчас — сен-окт 2026 по 150 (рост +50 %);
// свои продажи сен-окт 2026 по 15 (доля 10 %). Прогноз: 30 шт в день, 11.11.2026 — 90.
func knownForecast(t *testing.T) (last, now, own domain.DailySales) {
	last = series(t, day(2025, 9, 1), day(2025, 12, 31), func(d time.Time) float64 {
		switch {
		case d.Equal(day(2025, 11, 11)):
			return 600
		case d.Month() >= time.November:
			return 200
		}
		return 100
	})
	now = series(t, day(2026, 9, 1), day(2026, 10, 31), func(time.Time) float64 { return 150 })
	own = series(t, day(2026, 9, 1), day(2026, 10, 31), func(time.Time) float64 { return 15 })
	return last, now, own
}

func TestNewForecast_knownAnswer(t *testing.T) {
	last, now, own := knownForecast(t)
	f, err := domain.NewForecast(last, now, own)
	if err != nil {
		t.Fatalf("NewForecast: %v", err)
	}
	days := f.Days()
	if len(days) != 61 {
		t.Fatalf("days = %d, want 61 for ноя-дек 2026", len(days))
	}
	if !days[0].Day.Equal(day(2026, 11, 1)) || !days[60].Day.Equal(day(2026, 12, 31)) {
		t.Errorf("days from %v to %v, want 01.11.2026-31.12.2026", days[0].Day, days[60].Day)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	for _, d := range days {
		want := 30.0
		if d.Day.Equal(day(2026, 11, 11)) {
			want = 90
		}
		if !near(d.Value, want) {
			t.Errorf("%s = %v, want %v", d.Day.Format("02.01"), d.Value, want)
		}
		if d.Lo < 0 || d.Lo > d.Value || d.Hi < d.Value {
			t.Errorf("%s band %v..%v around %v", d.Day.Format("02.01"), d.Lo, d.Hi, d.Value)
		}
	}
	if days[20].Hi <= days[20].Value {
		t.Error("range is empty: last year has a spike, the band must be wider than zero")
	}
	if !near(f.Growth(), 0.5) || !near(f.Share(), 0.1) {
		t.Errorf("growth = %v, share = %v; want 0.5 and 0.1", f.Growth(), f.Share())
	}
	if p := f.Peak(); !p.Day.Equal(day(2026, 11, 11)) {
		t.Errorf("peak = %v, want 11.11.2026", p.Day)
	}
	if v, lo, hi := f.MonthTotal(2026, time.November); !near(v, 30*29+90) || lo > v || hi < v {
		t.Errorf("November = %v (%v..%v), want %v", v, lo, hi, 30*29+90)
	}
	if v, _, _ := f.MonthTotal(2026, time.December); !near(v, 30*31) {
		t.Errorf("December = %v, want %v", v, 30*31)
	}
	if lastDays := f.LastYear(); len(lastDays) != 61 || lastDays[10].Value != 60 {
		t.Errorf("last year in own scale = %d days, 11.11 = %v; want 61 and 600 × 0.1", len(lastDays), lastDays[10].Value)
	}
}

func TestNewForecast_shareOnOwnDays(t *testing.T) {
	last, now, _ := knownForecast(t)
	// Свои продажи только за октябрь — доля считается по тем же дням ниши.
	own := series(t, day(2026, 10, 1), day(2026, 10, 31), func(time.Time) float64 { return 30 })
	f, err := domain.NewForecast(last, now, own)
	if err != nil {
		t.Fatalf("NewForecast: %v", err)
	}
	if math.Abs(f.Share()-0.2) > 1e-9 {
		t.Errorf("share = %v, want 0.2 (30 из 150 в октябре)", f.Share())
	}
}

func TestNewForecast_refuses(t *testing.T) {
	last, now, own := knownForecast(t)
	onlyTarget := series(t, day(2025, 11, 1), day(2025, 12, 31), func(time.Time) float64 { return 200 })
	onlyBase := series(t, day(2025, 9, 1), day(2025, 10, 31), func(time.Time) float64 { return 100 })
	zeroNow := series(t, day(2026, 9, 1), day(2026, 10, 31), func(time.Time) float64 { return 0 })
	elsewhere := series(t, day(2026, 1, 1), day(2026, 1, 31), func(time.Time) float64 { return 5 })
	for _, tc := range []struct {
		name           string
		last, now, own domain.DailySales
	}{
		{"прошлый год без тех же дней, что сейчас", onlyTarget, now, own},
		{"прошлый год без дней после них", onlyBase, now, own},
		{"ниша сейчас пустая", last, domain.DailySales{}, own},
		{"ниша сейчас без продаж", last, zeroNow, own},
		{"своих продаж нет", last, now, domain.DailySales{}},
		{"свои продажи не в дни ниши", last, now, elsewhere},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewForecast(tc.last, tc.now, tc.own); !errors.Is(err, domain.ErrForecastInput) {
				t.Errorf("err = %v, want ErrForecastInput", err)
			}
		})
	}
}

func TestNewDailySales_rules(t *testing.T) {
	d := day(2026, 9, 1)
	for name, ds := range map[string][]domain.DaySales{
		"день дважды": {{Day: d, Value: 1}, {Day: d, Value: 2}},
		"меньше нуля": {{Day: d, Value: -1}},
		"без даты":    {{Value: 1}},
		"не число":    {{Day: d, Value: math.NaN()}},
	} {
		if _, err := domain.NewDailySales(ds); !errors.Is(err, domain.ErrForecastInput) {
			t.Errorf("%s: err = %v, want ErrForecastInput", name, err)
		}
	}
	s, err := domain.NewDailySales([]domain.DaySales{{Day: day(2026, 9, 2).Add(15 * time.Hour), Value: 2}, {Day: d, Value: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Days(); len(got) != 2 || !got[0].Day.Equal(d) || !got[1].Day.Equal(day(2026, 9, 2)) {
		t.Errorf("days = %+v, want sorted and cut to the date", got)
	}
}
