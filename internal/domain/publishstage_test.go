package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewPublishStage(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"draft", "draft", false},
		{"final", "final", false},
		{"published", "published", false},
		{"uppercase is not canonical", "Draft", true},
		{"unknown value", "bogus", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := domain.NewPublishStage(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidPublishStage) {
					t.Fatalf("NewPublishStage(%q) err = %v, want ErrInvalidPublishStage", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewPublishStage(%q) unexpected error: %v", tt.raw, err)
			}
			if ps.String() != tt.raw {
				t.Errorf("String() = %q, want %q", ps.String(), tt.raw)
			}
		})
	}
}
