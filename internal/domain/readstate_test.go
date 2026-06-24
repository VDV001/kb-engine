package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewReadState(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"read", "read", false},
		{"unread", "unread", false},
		{"uppercase is not canonical", "Read", true},
		{"unknown value", "bogus", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, err := domain.NewReadState(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidReadState) {
					t.Fatalf("NewReadState(%q) err = %v, want ErrInvalidReadState", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewReadState(%q) unexpected error: %v", tt.raw, err)
			}
			if rs.String() != tt.raw {
				t.Errorf("String() = %q, want %q", rs.String(), tt.raw)
			}
		})
	}
}
