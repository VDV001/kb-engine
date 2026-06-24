package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"active", "active", false},
		{"canonical", "canonical", false},
		{"outdated", "outdated", false},
		{"superseded", "superseded", false},
		{"dead-end", "dead-end", false},
		{"unknown value", "bogus", true},
		{"empty", "", true},
		{"wrong case is strict", "Active", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc, err := domain.NewLifecycle(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidLifecycle) {
					t.Fatalf("NewLifecycle(%q) err = %v, want ErrInvalidLifecycle", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLifecycle(%q) unexpected error: %v", tt.raw, err)
			}
			if lc.String() != tt.raw {
				t.Errorf("String() = %q, want %q", lc.String(), tt.raw)
			}
		})
	}
}

func TestLifecycle_IsOutdated(t *testing.T) {
	outdated, err := domain.NewLifecycle("outdated")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !outdated.IsOutdated() {
		t.Error("IsOutdated() = false for outdated, want true")
	}

	active, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if active.IsOutdated() {
		t.Error("IsOutdated() = true for active, want false")
	}
}

func TestLifecycle_IsCanonical(t *testing.T) {
	canonical, err := domain.NewLifecycle("canonical")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !canonical.IsCanonical() {
		t.Error("IsCanonical() = false for canonical, want true")
	}

	active, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if active.IsCanonical() {
		t.Error("IsCanonical() = true for active, want false")
	}
}
