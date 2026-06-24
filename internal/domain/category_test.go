package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewCategory(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"single word", "management", false},
		{"kebab-case", "ai-agents-tools", false},
		{"with digits", "k2-18", false},
		{"empty", "", true},
		{"uppercase rejected", "Golang", true},
		{"spaces rejected", "data science", true},
		{"underscore rejected", "data_science", true},
		{"leading hyphen rejected", "-management", true},
		{"trailing hyphen rejected", "management-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := domain.NewCategory(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidCategory) {
					t.Fatalf("NewCategory(%q) err = %v, want ErrInvalidCategory", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewCategory(%q) unexpected error: %v", tt.raw, err)
			}
			if c.String() != tt.raw {
				t.Errorf("String() = %q, want %q", c.String(), tt.raw)
			}
		})
	}
}
