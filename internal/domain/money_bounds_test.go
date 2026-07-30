package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// MoneyFromFloat is the one numeric path into an amount that had no boundary at
// all. It takes whatever strconv.ParseFloat accepted out of a spreadsheet cell,
// and the conversion to int64 saturates in silence:
//
//	"Inf"  → 9223372036854775807 kopecks — 92 quadrillion rubles, no error
//	1e17   → saturated at int64 max
//
// A cell holding text like that is unlikely in this book, which is why this is
// the last of the findings rather than the first. It is still the only amount
// path where a value the domain cannot represent produces a number instead of a
// refusal — and ParseMoney, right next to it, already refuses a third decimal
// digit on exactly that reasoning.
func TestMoneyFromFloat_refusesWhatItCannotRepresent(t *testing.T) {
	for name, f := range map[string]float64{
		"positive infinity": math.Inf(1),
		"negative infinity": math.Inf(-1),
		"not a number":      math.NaN(),
		"past int64":        1e17,
		"past int64 signed": -1e17,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := domain.MoneyFromFloat(f)
			if !errors.Is(err, domain.ErrInvalidMoney) {
				t.Fatalf("MoneyFromFloat(%v) = %v, %v — want ErrInvalidMoney", f, got, err)
			}
		})
	}
}

// String negates the amount to print a sign, and negating the most negative
// int64 leaves it negative — producing "--92233720368547758.-8", which is what
// then goes into the ledger's amount field and into every fingerprint built from
// it. Reachable through NewMoney, which takes kopecks from a caller.
func TestMoney_StringHandlesTheMostNegativeAmount(t *testing.T) {
	got := domain.NewMoney(math.MinInt64).String()
	const want = "-92233720368547758.08"
	if got != want {
		t.Errorf("NewMoney(MinInt64).String() = %q, want %q", got, want)
	}

	// Round-trips, which is the property the field is stored for.
	back, err := domain.ParseMoney(got)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", got, err)
	}
	if back.Kopecks() != math.MinInt64 {
		t.Errorf("ParseMoney(%q) = %d kopecks, want MinInt64", got, back.Kopecks())
	}
}

// ParseMoney multiplies the ruble part by 100, which overflows for a large but
// perfectly parseable integer — silently, into a small or negative amount.
func TestParseMoney_refusesAnAmountThatOverflows(t *testing.T) {
	for _, raw := range []string{
		"92233720368547759", // rubles × 100 exceeds int64
		"-92233720368547759",
		"9223372036854775807", // int64 max as rubles
	} {
		if got, err := domain.ParseMoney(raw); !errors.Is(err, domain.ErrInvalidMoney) {
			t.Errorf("ParseMoney(%q) = %v, %v — want ErrInvalidMoney", raw, got, err)
		}
	}
}
