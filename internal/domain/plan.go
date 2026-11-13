package domain

import (
	"errors"
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
	days  []PlanDay
	weeks []PlanWeek
}

// NewPlan builds the plan from the forecast.
func NewPlan(f Forecast, in PlanInput) (Plan, error) { return Plan{}, nil }

// Days returns the plan by day.
func (p Plan) Days() []PlanDay { return nil }

// Weeks returns the plan by week.
func (p Plan) Weeks() []PlanWeek { return nil }

// Sell is the plan's total sales, штук.
func (p Plan) Sell() float64 { return 0 }

// Profit is the plan's profit.
func (p Plan) Profit() float64 { return 0 }

// Supply — сколько довезти и к какой дате; ноль — остатка хватает.
func (p Plan) Supply() (qty float64, by time.Time) { return 0, time.Time{} }

// ShortForPeak — остаток кончится раньше дня пика или в него.
func (p Plan) ShortForPeak() bool { return false }
