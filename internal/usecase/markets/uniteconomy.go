package markets

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
)

// SKUEconomy — деньги товара за период из начислений Ozon (#580): те же строки,
// что «Деньги магазина», но по SKU и до рекламы — оплату за клик и продвижение
// Ozon начисляет на магазин, без товара.
type SKUEconomy struct {
	SKU                                                            string
	Units                                                          int // продано штук за вычетом возвратов
	Sales, Commission, Delivery, Promotion, Partners, Other, Total domain.Money
	UnitCost                                                       domain.Money // себестоимость штуки
	HasCost                                                        bool
}

// Cost — себестоимость проданного: штука × проданные штуки.
func (e SKUEconomy) Cost() domain.Money { return domain.Money{} }

// Profit — на счет минус себестоимость: прибыль товара до рекламы и налогов.
func (e SKUEconomy) Profit() domain.Money { return domain.Money{} }

// OzonSKUEconomy sums accruals by SKU; costOf — себестоимость штуки SKU.
func OzonSKUEconomy(acc []OzonAccrual, costOf func(sku string) (domain.Money, bool)) []SKUEconomy {
	return nil
}

// ErrNoCostColumns — в таблице нет колонок «артикул» и «себестоимость».
var ErrNoCostColumns = errors.New("в таблице нет колонок «артикул» и «себестоимость»")

// ReadCosts reads a cost table: строка заголовка с колонками «артикул» и
// «себестоимость», дальше по строке на артикул.
func ReadCosts(rows [][]string) (map[string]domain.Money, []Problem, error) {
	return nil, nil, nil
}
