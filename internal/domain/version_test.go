package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewVersion(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "three components", raw: "1.5.1", want: "1.5.1", ok: true},
		{name: "zero major is a real version", raw: "0.1.0", want: "0.1.0", ok: true},
		{name: "multi-digit components", raw: "10.20.30", want: "10.20.30", ok: true},
		{name: "two components are not a version", raw: "1.0"},
		{name: "a bare number is a revision, not a version", raw: "1"},
		{name: "four components", raw: "1.2.3.4"},
		{name: "leading v is decoration", raw: "v1.2.3"},
		{name: "empty", raw: ""},
		{name: "not a number", raw: "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := domain.NewVersion(c.raw)
			if !c.ok {
				if err == nil {
					t.Fatalf("NewVersion(%q) = %v, want error", c.raw, got)
				}
				if !errors.Is(err, domain.ErrInvalidVersion) {
					t.Fatalf("NewVersion(%q) error = %v, want ErrInvalidVersion", c.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewVersion(%q) unexpected error: %v", c.raw, err)
			}
			if got.String() != c.want {
				t.Fatalf("NewVersion(%q).String() = %q, want %q", c.raw, got.String(), c.want)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	// The audit compares the catalog's copy against the artefact file. It must
	// answer «behind» and not merely «different», or a version that moved
	// backwards would read the same as one that moved forward.
	cases := []struct {
		name     string
		a, b     string
		wantSign int
	}{
		{name: "equal", a: "1.5.1", b: "1.5.1", wantSign: 0},
		{name: "patch behind", a: "1.5.0", b: "1.5.1", wantSign: -1},
		{name: "minor behind", a: "1.3.0", b: "1.5.1", wantSign: -1},
		{name: "major ahead", a: "2.0.0", b: "1.9.9", wantSign: 1},
		{name: "numeric not lexicographic", a: "1.10.0", b: "1.9.0", wantSign: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := domain.NewVersion(c.a)
			if err != nil {
				t.Fatalf("NewVersion(%q): %v", c.a, err)
			}
			b, err := domain.NewVersion(c.b)
			if err != nil {
				t.Fatalf("NewVersion(%q): %v", c.b, err)
			}
			if got := a.Compare(b); got != c.wantSign {
				t.Fatalf("%q.Compare(%q) = %d, want %d", c.a, c.b, got, c.wantSign)
			}
		})
	}
}
