package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidMoney is returned when a raw amount cannot be read as an exact
// value in kopecks.
var ErrInvalidMoney = errors.New("invalid money")

// Money is an exact monetary amount held in kopecks.
//
// Kopecks rather than float64 on purpose: 125 of the 507 expense rows in the
// real ledger carry decimals, and binary floating point cannot represent 0.1
// exactly — summing such a ledger drifts. Integer kopecks make every total
// reproducible, which is what a finance report has to be.
type Money struct {
	kopecks int64
}

// NewMoney builds an amount from an exact number of kopecks.
func NewMoney(kopecks int64) Money { return Money{kopecks: kopecks} }

// MoneyFromFloat converts a spreadsheet amount to exact kopecks.
//
// Separate from ParseMoney on purpose. ParseMoney reads text a person typed and
// refuses anything finer than a kopeck, because "10.005" is a mistake worth
// surfacing. A float comes from storage, where 89.99 is genuinely held as
// 89.98999999999999 — that is representation noise, not intent, so it rounds to
// the nearest kopeck (halves away from zero) instead of being rejected.
//
// A value that does not fit in kopecks is refused rather than saturated. The
// bound is int64 itself, not a guess at how much money is plausible: the domain
// either represents the amount exactly or says it cannot. One comparison covers
// NaN and both infinities too, since every comparison against NaN is false.
func MoneyFromFloat(f float64) (Money, error) {
	k := math.Round(f * 100)
	// float64(math.MaxInt64) rounds up to 2^63, so a strict < is the exact
	// boundary; MinInt64 is representable and stays inclusive.
	if !(k >= math.MinInt64 && k < -float64(math.MinInt64)) {
		return Money{}, fmt.Errorf("%w: %v is not an amount in kopecks", ErrInvalidMoney, f)
	}
	return Money{kopecks: int64(k)}, nil
}

// Kopecks returns the amount as a whole number of kopecks.
func (m Money) Kopecks() int64 { return m.kopecks }

// Add returns the sum of two amounts. Exact by construction.
//
// ponytail: int64 addition, so two amounts near the top of the range wrap
// silently — 92233720368547758.07 twice over reports -0.02. Not guarded, because
// a transaction that large is refused by NewTransaction (an amount has to carry a
// sign) and the real ledger totals eight digits of kopecks, four orders of
// magnitude short of a wrap even summed a million times. The upgrade path is a
// checked add returning an error, which would touch every caller of Summarize;
// the ceiling to watch is a single amount above ~9.2e16 kopecks entering through
// NewMoney, the one constructor with no bound.
func (m Money) Add(other Money) Money { return Money{kopecks: m.kopecks + other.kopecks} }

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m.kopecks == 0 }

// String renders the amount with two decimal places and no thousands
// separators, e.g. "166703.82". Suitable for storage; formatting for humans
// belongs to the presentation layer.
//
// The magnitude is taken in uint64, not by negating in int64: negating the most
// negative int64 leaves it negative, and this string is what the ledger stores
// in its amount field and what every fingerprint is built from.
func (m Money) String() string {
	sign, abs := "", uint64(m.kopecks)
	if m.kopecks < 0 {
		// -(k+1)+1 keeps the intermediate inside int64 for every input, MinInt64
		// included.
		sign, abs = "-", uint64(-(m.kopecks+1))+1
	}
	return fmt.Sprintf("%s%d.%02d", sign, abs/100, abs%100)
}

// moneyCleaner strips the decorations a spreadsheet cell may carry: the ruble
// sign and every flavour of grouping space (regular, non-breaking, narrow
// non-breaking) that LibreOffice and Excel emit.
var moneyCleaner = strings.NewReplacer(
	" ", "",
	" ", "",
	" ", "",
	"₽", "",
)

// ParseMoney reads a raw amount into exact kopecks.
//
// Accepts what the ledger actually contains: plain numbers, a comma or a dot as
// the decimal separator, grouping spaces and a trailing currency sign. One
// decimal digit means tenths ("12.5" is 12 rubles 50 kopecks).
//
// Three or more decimal digits are rejected rather than rounded: silently
// dropping a fraction of a kopeck is how a ledger stops reconciling, and the
// caller is better off seeing the bad cell.
func ParseMoney(raw string) (Money, error) {
	s := moneyCleaner.Replace(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return Money{}, fmt.Errorf("%w: empty amount", ErrInvalidMoney)
	}

	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")

	whole, frac, hasFrac := strings.Cut(s, ".")
	if hasFrac && len(frac) > 2 {
		return Money{}, fmt.Errorf("%w: %q has more precision than a kopeck", ErrInvalidMoney, raw)
	}

	rubles, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidMoney, raw)
	}

	var kopecks int64
	if hasFrac {
		// "5" means 50 kopecks, "05" means 5 — pad on the right, not the left.
		padded := frac + strings.Repeat("0", 2-len(frac))
		if kopecks, err = strconv.ParseInt(padded, 10, 64); err != nil {
			return Money{}, fmt.Errorf("%w: %q", ErrInvalidMoney, raw)
		}
	}

	// The magnitude is built in uint64 and bounded before the multiply, because
	// rubles×100 wraps into a small or negative amount long before rubles itself
	// stops fitting in an int64.
	//
	// The negative side reaches one kopeck further than the positive one: MinInt64
	// has no positive counterpart. That extra step is not a curiosity — String can
	// emit "-92233720368547758.08", and a value this package can write has to be
	// one it can read back.
	maxMag := uint64(math.MaxInt64)
	if neg {
		maxMag++
	}
	mag := uint64(rubles)
	if mag > (maxMag-uint64(kopecks))/100 {
		return Money{}, fmt.Errorf("%w: %q is larger than an amount in kopecks can hold", ErrInvalidMoney, raw)
	}
	mag = mag*100 + uint64(kopecks)

	if neg {
		// Stepping down from -1 rather than negating, so the intermediate stays
		// inside int64 even when mag is exactly 2^63.
		return Money{kopecks: -int64(mag-1) - 1}, nil
	}
	return Money{kopecks: int64(mag)}, nil
}
