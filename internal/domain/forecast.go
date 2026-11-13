package domain

import (
	"errors"
	"time"
)

// ErrForecastInput is returned when the sales series cannot give a forecast.
var ErrForecastInput = errors.New("для прогноза не хватает данных")

// DaySales — продажи за день.
type DaySales struct {
	Day   time.Time
	Value float64
}

// DailySales — продажи по дням, по возрастанию даты, день один раз.
type DailySales struct{ days []DaySales }

// NewDailySales checks and sorts daily sales.
func NewDailySales(ds []DaySales) (DailySales, error) { return DailySales{}, nil }

// Days returns the days in date order.
func (s DailySales) Days() []DaySales { return nil }

// ForecastDay — прогноз на день и диапазон.
type ForecastDay struct {
	Day           time.Time
	Value, Lo, Hi float64
}

// Forecast — прогноз своих продаж по дням.
type Forecast struct{}

// NewForecast forecasts own sales.
func NewForecast(lastYear, nicheNow, own DailySales) (Forecast, error) { return Forecast{}, nil }

// Days returns the forecast days.
func (f Forecast) Days() []ForecastDay { return nil }

// LastYear returns last year's niche curve in own scale on the forecast days.
func (f Forecast) LastYear() []DaySales { return nil }

// Growth is niche growth to last year.
func (f Forecast) Growth() float64 { return 0 }

// Share is own share of the niche.
func (f Forecast) Share() float64 { return 0 }

// Peak is the day with the highest forecast.
func (f Forecast) Peak() ForecastDay { return ForecastDay{} }

// MonthTotal sums a month of the forecast.
func (f Forecast) MonthTotal(year int, m time.Month) (v, lo, hi float64) { return 0, 0, 0 }
