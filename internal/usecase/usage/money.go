package usage

import (
	"cmp"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// PriceBook gives the price of a model; нет цены — false, а не ноль.
type PriceBook interface {
	Price(model string) (domain.ModelPrice, bool)
}

// PurposeCost is the month's spend of one purpose.
type PurposeCost struct {
	Purpose domain.CallPurpose
	Cost    int64 // пикодолларов
	Calls   int
}

// ModelCost is the month's spend of one model; Priced — цена модели известна.
type ModelCost struct {
	Model   string
	Cost    int64
	Calls   int
	Counted int // вызовов со счетом: только у них цена что-то меняет
	Priced  bool
}

// Money is the spend of the current month in the person's time zone (экран
// «Деньги», #493): по вызовам со счетом и ценой либо с ценой от провайдера;
// без счета и без цены —
// отдельными числами, чтобы «неизвестно» не выглядело бесплатным.
type Money struct {
	Month                   string // ГГГГ-ММ
	DaysPassed, DaysInMonth int
	Spent, Forecast         int64   // пикодолларов
	Days                    []int64 // расход по дням с 1-го по сегодня
	Purposes                []PurposeCost
	Models                  []ModelCost
	Uncounted, Unpriced     int
}

// NewMoney builds the month's spend up to now. Прогноз — средний расход
// прошедших дней (сегодня считается), растянутый на весь месяц.
func NewMoney(calls []domain.ModelCall, now time.Time, prices PriceBook) Money {
	loc := now.Location()
	y, mo, d := now.Date()
	start := time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	m := Money{Month: start.Format("2006-01"), DaysPassed: d, DaysInMonth: start.AddDate(0, 1, -1).Day(), Days: make([]int64, d)}
	byPurpose := map[domain.CallPurpose]*PurposeCost{}
	byModel := map[string]*ModelCost{}
	for _, c := range calls {
		at := c.At().In(loc)
		if at.Before(start) || at.Year() != y || at.Month() != mo || at.Day() > d {
			continue // прошлый месяц или из будущего: часы Мака сбились
		}
		pc := byPurpose[c.Purpose()]
		if pc == nil {
			pc = &PurposeCost{Purpose: c.Purpose()}
			byPurpose[c.Purpose()] = pc
		}
		mc := byModel[c.Model()]
		if mc == nil {
			mc = &ModelCost{Model: c.Model()}
			byModel[c.Model()] = mc
		}
		pc.Calls++
		mc.Calls++
		var cost int64
		if c.Billed() { // цену прислал провайдер: цена модели не нужна
			mc.Priced = true
			mc.Counted++
			cost = c.Cost()
		} else {
			p, ok := prices.Price(c.Model())
			mc.Priced = ok
			if !c.Counted() {
				m.Uncounted++
				continue
			}
			mc.Counted++
			if !ok {
				m.Unpriced++
				continue
			}
			cost = p.Cost(c.In(), c.Out())
		}
		m.Spent += cost
		m.Days[at.Day()-1] += cost
		pc.Cost += cost
		mc.Cost += cost
	}
	m.Forecast = m.Spent * int64(m.DaysInMonth) / int64(m.DaysPassed)
	for _, p := range byPurpose {
		m.Purposes = append(m.Purposes, *p)
	}
	slices.SortFunc(m.Purposes, func(a, b PurposeCost) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Purpose, b.Purpose))
	})
	for _, x := range byModel {
		m.Models = append(m.Models, *x)
	}
	slices.SortFunc(m.Models, func(a, b ModelCost) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Model, b.Model))
	})
	return m
}
