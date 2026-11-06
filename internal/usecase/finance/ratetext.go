package finance

import (
	"math/big"
	"strings"
)

// RateText shows an exchange rate with two decimals, as money is read, and a
// cheaper unit with four significant digits: курс сума — «0.007502», а не
// «0.01». Одна функция на все витрины — сервер, fin add, fin report: курс одной
// записи не должен читаться по-разному в двух местах.
func RateText(r *big.Rat) string {
	if r.Cmp(big.NewRat(1, 1)) >= 0 {
		return r.FloatString(2)
	}
	for decimals := 2; decimals < 20; decimals++ {
		s := r.FloatString(decimals)
		if len(strings.TrimLeft(strings.ReplaceAll(s, ".", ""), "0")) >= 4 {
			return s
		}
	}
	return r.FloatString(20)
}
