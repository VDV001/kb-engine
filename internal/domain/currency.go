package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCurrency is returned when a currency code violates an invariant.
var ErrInvalidCurrency = errors.New("invalid currency")

// ErrInvalidRate is returned when an exchange rate violates an invariant.
var ErrInvalidRate = errors.New("invalid rate")

// baseCurrency is the unit the book is kept in. Everything else is valued
// against it.
const baseCurrency = "RUB"

// Currency is the unit an account is held in.
//
// Нулевое значение — рубль, и это решение, а не побочный эффект. Лист «Счета»
// существует в единственном экземпляре и колонки валюты не имеет; весь код,
// написанный до #332, строит счета вообще ничего не зная о валюте. Умолчание
// обязано быть тем, чем эти счета пользовались, иначе обратная совместимость
// держалась бы на том, что каждый вызывающий не забыл передать RUB.
//
// Из этого следует инвариант, который проверяется тестом: NewCurrency("RUB")
// обязана давать значение, РАВНОЕ нулевому. Иначе один и тот же рублёвый счёт,
// пришедший из старой книги и из новой, оказался бы двумя разными счетами при
// сравнении структур.
type Currency struct {
	code string
}

// NewCurrency validates and returns a currency code.
//
// Проверяется форма, а не принадлежность к списку: перечень ISO-4217 живёт вне
// движка и меняется без него, а гейт, отвергающий настоящую валюту из-за
// устаревшей копии списка, будет обойдён вызывающим. Домен отвечает за то, что
// в поле лежит код валюты, а не за то, что такая валюта существует.
func NewCurrency(code string) (Currency, error) {
	c := strings.ToUpper(strings.TrimSpace(code))
	if len(c) != 3 {
		return Currency{}, fmt.Errorf("%w: %q is not a three-letter code", ErrInvalidCurrency, code)
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return Currency{}, fmt.Errorf("%w: %q must be latin letters", ErrInvalidCurrency, code)
		}
	}
	if c == baseCurrency {
		return Currency{}, nil // канонизируем в нулевое значение
	}
	return Currency{code: c}, nil
}

// Code returns the three-letter code; the zero value reads as the base currency.
func (c Currency) Code() string {
	if c.code == "" {
		return baseCurrency
	}
	return c.code
}

// IsBase reports whether the account is held in the currency the book is kept
// in — that is, whether its balance needs no valuation at all.
func (c Currency) IsBase() bool { return c.code == "" }

// String implements fmt.Stringer.
func (c Currency) String() string { return c.Code() }

// Rate is how much of the base currency one unit of another currency was worth
// at the moment somebody wrote it down.
//
// Отдельный тип, а не Money, по двум причинам. Курс — это отношение, а не
// сумма: складывать курсы бессмысленно, и Money разрешил бы это молча. И
// главное — «курс неизвестен» обязано быть отдельным ответом, а не нулём:
// подставленный ноль обнулил бы оценку, а подставленный «последний известный»
// выдал бы за замер число, которого никто не измерял.
type Rate struct {
	known   bool
	perUnit Money
}

// UnknownRate returns the answer "nobody wrote the rate down".
//
// Это не отказ и не ошибка: у наличной валюты, полученной подарком, курса
// может не быть вовсе. Витрина обязана сказать «оценка неизвестна», а не
// показать ноль.
func UnknownRate() Rate { return Rate{} }

// NewRate validates and returns how much base currency one unit is worth.
func NewRate(perUnit Money) (Rate, error) {
	if perUnit.Kopecks() <= 0 {
		return Rate{}, fmt.Errorf("%w: %d kopecks per unit is not a rate", ErrInvalidRate, perUnit.Kopecks())
	}
	return Rate{known: true, perUnit: perUnit}, nil
}

// Known reports whether the rate was written down.
func (r Rate) Known() bool { return r.known }

// PerUnit returns how much base currency one unit is worth, and whether that is
// known at all.
//
// Второе значение возвращается намеренно вместо голого Money: вызывающий,
// забывший спросить Known(), получил бы ноль и принял его за курс.
func (r Rate) PerUnit() (Money, bool) {
	if !r.known {
		return Money{}, false
	}
	return r.perUnit, true
}

// Apply values an amount in this currency against the base currency.
//
// Второе значение говорит, известен ли курс вообще: у наличной валюты его
// может не быть, и ноль здесь читался бы как «денег нет».
//
// Живёт в домене, а не у вызывающего: умножение с проверкой переполнения — то
// самое место, которое каждая витрина, посчитав сама, однажды посчитает иначе.
func (r Rate) Apply(amount Money) (Money, bool) {
	per, ok := r.PerUnit()
	if !ok {
		return Money{}, false
	}
	// И сумма, и курс лежат в сотых долях, поэтому произведение выходит в
	// десятитысячных и делится на сто. Переполнение проверяется, а не
	// предполагается: молча завернувшееся int64 дало бы отрицательный остаток
	// на счёте, который просто велик.
	units, perUnit := amount.Kopecks(), per.Kopecks()
	product := units * perUnit
	if units != 0 && product/units != perUnit {
		return Money{}, false
	}
	// Округление половин от нуля — как в MoneyFromFloat, чтобы одна и та же
	// сумма не зависела от того, каким путём она пришла.
	half := int64(50)
	if product < 0 {
		half = -50
	}
	return NewMoney((product + half) / 100), true
}
