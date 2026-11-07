package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Цена модели для экрана «Деньги» (#493, решение владельца 07.11: цены из
// шлюза, своя правка в настройках). Внутри — пикодоллары за токен: суммы
// месяца складываются целыми и не копят ошибку округления.

// ErrInvalidModelPrice — цена не число, отрицательная или точнее пикодоллара.
var ErrInvalidModelPrice = errors.New("invalid model price")

// ModelPrice is the price of a model's input and output tokens.
type ModelPrice struct {
	in, out int64 // пикодолларов за токен
}

// maxDollars bounds the whole part: дороже $1 млн за единицу — опечатка, а не цена.
const maxDollars = 1_000_000

// ParseModelPricePerToken reads dollars per token, as the gateway sends them ("0.0000001").
func ParseModelPricePerToken(in, out string) (ModelPrice, error) { return parsePrice(in, out, 12) }

// ParseModelPricePerMillion reads dollars per million tokens, as a person types them ("0,10").
func ParseModelPricePerMillion(in, out string) (ModelPrice, error) { return parsePrice(in, out, 6) }

// ParseDollars reads the price of one call in dollars, as the gateway sends it ("0.000335").
func ParseDollars(s string) (int64, error) {
	v, err := parseDecimal(s, 12)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrInvalidModelPrice, s, err)
	}
	return v, nil
}

func parsePrice(in, out string, scale int) (ModelPrice, error) {
	i, err := parseDecimal(in, scale)
	if err != nil {
		return ModelPrice{}, fmt.Errorf("%w: вход %q: %w", ErrInvalidModelPrice, in, err)
	}
	o, err := parseDecimal(out, scale)
	if err != nil {
		return ModelPrice{}, fmt.Errorf("%w: выход %q: %w", ErrInvalidModelPrice, out, err)
	}
	return ModelPrice{in: i, out: o}, nil
}

// parseDecimal returns s × 10^scale: только цифры и одна точка или запятая;
// знак, экспонента и цифры дальше scale (кроме нулей) — отказ.
func parseDecimal(s string, scale int) (int64, error) {
	s = strings.TrimSpace(strings.Replace(s, ",", ".", 1))
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" {
		return 0, errors.New("пусто")
	}
	digits := func(x string) bool { return strings.Trim(x, "0123456789") == "" }
	if !digits(whole) || !digits(frac) {
		return 0, errors.New("не число")
	}
	if extra := strings.TrimRight(frac, "0"); len(extra) > scale {
		return 0, fmt.Errorf("точнее 10^-%d", scale)
	}
	frac = (frac + strings.Repeat("0", scale))[:scale]
	w := int64(0)
	if whole != "" {
		var err error
		if w, err = strconv.ParseInt(whole, 10, 64); err != nil || w > maxDollars {
			return 0, errors.New("слишком много")
		}
	}
	f, _ := strconv.ParseInt(frac, 10, 64) // только цифры, не длиннее 12
	pow := int64(1)
	for range scale {
		pow *= 10
	}
	return w*pow + f, nil
}

// Cost returns the price of a call in picodollars.
func (p ModelPrice) Cost(in, out int) int64 { return int64(in)*p.in + int64(out)*p.out }

// PerMillion returns dollars per million input and output tokens, as the settings show them.
func (p ModelPrice) PerMillion() (in, out string) { return dollars6(p.in), dollars6(p.out) }

// dollars6 prints picodollars per token as dollars per million: то же число, сдвинутое на 6 знаков.
func dollars6(pico int64) string {
	s := strconv.FormatInt(pico/1_000_000, 10)
	if f := strings.TrimRight(fmt.Sprintf("%06d", pico%1_000_000), "0"); f != "" {
		s += "." + f
	}
	return s
}
