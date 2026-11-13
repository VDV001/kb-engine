package markets

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// SKUEconomy — деньги товара за период из начислений Ozon (#580): те же строки,
// что «Деньги магазина», но по SKU и до рекламы — оплату за клик и продвижение
// Ozon начисляет на магазин, без товара.
type SKUEconomy struct {
	SKU   string
	Units int // продано штук за вычетом возвратов
	ShopEconomy
	UnitCost domain.Money // себестоимость штуки
	HasCost  bool
}

// Cost — себестоимость проданного: штука × проданные штуки.
func (e SKUEconomy) Cost() domain.Money {
	if !e.HasCost {
		return domain.Money{}
	}
	return domain.NewMoney(e.UnitCost.Kopecks() * int64(e.Units))
}

// Profit — на счет минус себестоимость: прибыль товара до рекламы и налогов.
func (e SKUEconomy) Profit() domain.Money { return e.Total.Sub(e.Cost()) }

// OzonSKUEconomy sums accruals by SKU, больше на счет — выше; costOf —
// себестоимость штуки SKU. Строки без SKU (реклама, документы) — не товар.
func OzonSKUEconomy(acc []OzonAccrual, costOf func(sku string) (domain.Money, bool)) []SKUEconomy {
	at := map[string]int{}
	var out []SKUEconomy
	for _, a := range acc {
		if a.SKU == "" {
			continue
		}
		i, ok := at[a.SKU]
		if !ok {
			i = len(out)
			at[a.SKU] = i
			out = append(out, SKUEconomy{SKU: a.SKU})
		}
		e := &out[i]
		e.add(a)
		if a.Kind == AccrualSale { // продажа — плюс штуки, возврат (минус) — минус
			switch {
			case a.Amount.Kopecks() > 0:
				e.Units += a.Qty
			case a.Amount.Kopecks() < 0:
				e.Units -= a.Qty
			}
		}
	}
	for i := range out {
		out[i].UnitCost, out[i].HasCost = costOf(out[i].SKU)
	}
	slices.SortFunc(out, func(a, b SKUEconomy) int {
		return cmp.Or(cmp.Compare(b.Total.Kopecks(), a.Total.Kopecks()), cmp.Compare(a.SKU, b.SKU))
	})
	return out
}

// ErrNoCostColumns — в таблице нет колонок «артикул» и «себестоимость».
var ErrNoCostColumns = errors.New("в таблице нет колонок «артикул» и «себестоимость»")

// ReadCosts reads a cost table: строка заголовка с колонками «артикул» и
// «себестоимость», дальше по строке на артикул. Сумма — как ее пишет человек
// («1 250,50 ₽») или число из Excel (до копейки). Номера строк — с 1, как в Excel.
func ReadCosts(rows [][]string) (map[string]domain.Money, []Problem, error) {
	h, ac, cc := costHeader(rows)
	if h < 0 {
		return nil, nil, ErrNoCostColumns
	}
	out := map[string]domain.Money{}
	var probs []Problem
	for i := h + 1; i < len(rows); i++ {
		art, raw := strings.TrimSpace(cell(rows[i], ac)), strings.TrimSpace(cell(rows[i], cc))
		if art == "" && raw == "" {
			continue
		}
		m, why := costOf(art, raw)
		if why != "" {
			probs = append(probs, Problem{Row: i + 1, Reason: why})
			continue
		}
		out[art] = m
	}
	return out, probs, nil
}

// costHeader finds the header row and its two columns; -1 — нет.
func costHeader(rows [][]string) (row, art, cost int) {
	for i, r := range rows {
		art, cost = -1, -1
		for j, c := range r {
			c = strings.ToLower(c)
			switch {
			case art < 0 && strings.Contains(c, "артикул"):
				art = j
			case cost < 0 && strings.Contains(c, "себестоим"):
				cost = j
			}
		}
		if art >= 0 && cost >= 0 {
			return i, art, cost
		}
	}
	return -1, -1, -1
}

// ErrBadCost — себестоимость не число или меньше нуля.
var ErrBadCost = errors.New("себестоимость не принята")

// ParseCost reads one cost as a person types it: «1 250,50», «480 ₽».
func ParseCost(article, raw string) (domain.Money, error) {
	m, why := costOf(article, strings.TrimSpace(raw))
	if why != "" {
		return domain.Money{}, fmt.Errorf("%w: %s", ErrBadCost, why)
	}
	return m, nil
}

// costOf reads one row's cost; why — почему строка не принята.
func costOf(art, raw string) (domain.Money, string) {
	switch {
	case art == "":
		return domain.Money{}, "нет артикула"
	case raw == "":
		return domain.Money{}, fmt.Sprintf("у «%s» нет себестоимости", art)
	}
	m, err := domain.ParseMoney(raw)
	if err != nil { // число из Excel с хвостом дробей — до копейки
		f, ferr := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
		if ferr != nil {
			return domain.Money{}, fmt.Sprintf("у «%s» себестоимость не число: «%s»", art, raw)
		}
		if m, err = domain.MoneyFromFloat(f); err != nil {
			return domain.Money{}, fmt.Sprintf("у «%s» себестоимость не число: «%s»", art, raw)
		}
	}
	if m.Kopecks() < 0 {
		return domain.Money{}, fmt.Sprintf("у «%s» себестоимость меньше нуля", art)
	}
	return m, ""
}
