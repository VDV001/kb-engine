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

// ChatCost is the month's spend of one chat (#541).
type ChatCost struct {
	Chat  string
	Cost  int64 // пикодолларов
	Calls int
}

// ModelCost is the month's spend of one model; Priced — цена модели известна.
type ModelCost struct {
	Model   string
	Cost    int64
	Calls   int
	Counted int  // вызовов со счетом токенов: только у них цена модели что-то меняет
	Billed  int  // вызовов с ценой от провайдера: цена модели им не нужна
	Priced  bool // расход известен: есть цена модели или счет провайдера
}

// add counts one call of the model; от порядка вызовов в журнале не зависит.
func (mc *ModelCost) add(st costState, priced bool) {
	mc.Calls++
	mc.Priced = mc.Priced || priced
	switch st {
	case costBilled:
		mc.Billed++
	case costKnown, costUnpriced:
		mc.Counted++
	}
}

// Money is the spend of the current month in the person's time zone (экран
// «Деньги», #493): по вызовам со счетом и ценой модели и по вызовам с ценой
// от провайдера (облачная речь у шлюза, цена модели им не нужна); без счета и
// без цены — отдельными числами, чтобы «неизвестно» не выглядело бесплатным.
type Money struct {
	Month                   string // ГГГГ-ММ
	DaysPassed, DaysInMonth int
	Spent, Forecast         int64   // пикодолларов
	Days                    []int64 // расход по дням с 1-го по сегодня
	Purposes                []PurposeCost
	Models                  []ModelCost
	Chats                   []ChatCost // от дорогих к дешевым; вызовы вне чата сюда не входят
	Uncounted, Unpriced     int
}

// costState says whether the price of a call is known.
type costState int

const (
	costKnown     costState = iota
	costBilled              // цену прислал провайдер
	costUncounted           // счета нет: токены неизвестны
	costUnpriced            // счет есть, цены модели нет
)

// callCost returns the price of a call: цену прислал провайдер — она, иначе
// токены по цене модели; priced — у модели есть цена либо вызов с ценой.
func callCost(c domain.ModelCall, prices PriceBook) (cost int64, priced bool, st costState) {
	if c.Billed() { // цена модели не нужна
		return c.Cost(), true, costBilled
	}
	p, ok := prices.Price(c.Model())
	switch {
	case !c.Counted():
		return 0, ok, costUncounted
	case !ok:
		return 0, false, costUnpriced
	}
	return p.Cost(c.In(), c.Out()), true, costKnown
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
	byChat := map[string]*ChatCost{}
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
		var cc *ChatCost
		if c.Chat() != "" {
			if cc = byChat[c.Chat()]; cc == nil {
				cc = &ChatCost{Chat: c.Chat()}
				byChat[c.Chat()] = cc
			}
			cc.Calls++
		}
		cost, priced, st := callCost(c, prices)
		mc.add(st, priced)
		switch st {
		case costUncounted:
			m.Uncounted++
			continue
		case costUnpriced:
			m.Unpriced++
			continue
		}
		m.Spent += cost
		m.Days[at.Day()-1] += cost
		pc.Cost += cost
		mc.Cost += cost
		if cc != nil {
			cc.Cost += cost
		}
	}
	m.Forecast = m.Spent * int64(m.DaysInMonth) / int64(m.DaysPassed)
	for _, p := range byPurpose {
		m.Purposes = append(m.Purposes, *p)
	}
	slices.SortFunc(m.Purposes, func(a, b PurposeCost) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Purpose, b.Purpose))
	})
	for _, x := range byChat {
		m.Chats = append(m.Chats, *x)
	}
	slices.SortFunc(m.Chats, func(a, b ChatCost) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Chat, b.Chat))
	})
	for _, x := range byModel {
		m.Models = append(m.Models, *x)
	}
	slices.SortFunc(m.Models, func(a, b ModelCost) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Model, b.Model))
	})
	return m
}
