package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestParseMoney(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64 // kopecks
		wantErr bool
	}{
		{"whole rubles", "500", 50000, false},
		{"two decimals", "166703.82", 16670382, false},
		{"one decimal is tenths, not hundredths", "12.5", 1250, false},
		{"comma as separator", "202,45", 20245, false},
		{"spaces are grouping", "1 300", 130000, false},
		{"non-breaking space", "1 300", 130000, false},
		{"currency suffix", "500 ₽", 50000, false},
		{"zero", "0", 0, false},
		{"negative", "-250.10", -25010, false},
		{"empty", "", 0, true},
		{"not a number", "abc", 0, true},
		{"three decimals lose money silently", "10.005", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseMoney(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseMoney(%q) = %v, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMoney(%q): %v", tt.raw, err)
			}
			if got.Kopecks() != tt.want {
				t.Errorf("ParseMoney(%q) = %d kopecks, want %d", tt.raw, got.Kopecks(), tt.want)
			}
		})
	}
}

// Floats are the classic way to lose a kopeck. 0.1+0.2 != 0.3 in binary
// floating point, and 125 of the 507 real expense rows carry decimals — so the
// engine must never route an amount through float64 arithmetic.
func TestMoney_arithmeticIsExact(t *testing.T) {
	a, err := domain.ParseMoney("0.1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.ParseMoney("0.2")
	if err != nil {
		t.Fatal(err)
	}
	if sum := a.Add(b); sum.Kopecks() != 30 {
		t.Errorf("0.1 + 0.2 = %d kopecks, want 30", sum.Kopecks())
	}
}

func TestMoney_String(t *testing.T) {
	tests := []struct {
		kopecks int64
		want    string
	}{
		{50000, "500.00"},
		{16670382, "166703.82"},
		{-25010, "-250.10"},
		{5, "0.05"},
		{0, "0.00"},
	}
	for _, tt := range tests {
		if got := domain.NewMoney(tt.kopecks).String(); got != tt.want {
			t.Errorf("NewMoney(%d).String() = %q, want %q", tt.kopecks, got, tt.want)
		}
	}
}
