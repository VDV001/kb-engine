package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewScope(t *testing.T) {
	cases := []struct {
		in    string
		want  domain.Scope
		write bool
		bad   bool
	}{
		{"read", domain.ScopeRead, false, false},
		{"write", domain.ScopeWrite, true, false},
		{"", domain.ScopeWrite, true, false},
		{"Read", domain.Scope{}, false, true},
		{"readonly", domain.Scope{}, false, true},
		{"admin", domain.Scope{}, false, true},
		{" read", domain.Scope{}, false, true},
	}
	for _, c := range cases {
		got, err := domain.NewScope(c.in)
		if c.bad {
			if !errors.Is(err, domain.ErrInvalidScope) {
				t.Errorf("NewScope(%q) = %v, want ErrInvalidScope", c.in, err)
			}
			continue
		}
		if err != nil || got != c.want || got.AllowsWrite() != c.write {
			t.Errorf("NewScope(%q) = %v, %v; want %v, write=%v", c.in, got, err, c.want, c.write)
		}
	}
	if domain.ScopeRead.String() != "read" || domain.ScopeWrite.String() != "write" {
		t.Errorf("String: %q %q", domain.ScopeRead, domain.ScopeWrite)
	}
}
