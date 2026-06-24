package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewVerdict(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"keep", "keep", false},
		{"napodumat", "napodumat", false},
		{"skip", "skip", false},
		{"skip-unavailable", "skip-unavailable", false},
		{"uppercase is not canonical", "KEEP", true},
		{"cyrillic legacy is not canonical", "на подумать", true},
		{"unknown value", "bogus", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := domain.NewVerdict(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidVerdict) {
					t.Fatalf("NewVerdict(%q) err = %v, want ErrInvalidVerdict", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewVerdict(%q) unexpected error: %v", tt.raw, err)
			}
			if v.String() != tt.raw {
				t.Errorf("String() = %q, want %q", v.String(), tt.raw)
			}
		})
	}
}

func TestVerdict_IsSkipUnavailable(t *testing.T) {
	su, err := domain.NewVerdict("skip-unavailable")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !su.IsSkipUnavailable() {
		t.Error("IsSkipUnavailable() = false for skip-unavailable, want true")
	}

	keep, err := domain.NewVerdict("keep")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if keep.IsSkipUnavailable() {
		t.Error("IsSkipUnavailable() = true for keep, want false")
	}
}
