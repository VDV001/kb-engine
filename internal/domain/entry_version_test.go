package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func mustVersion(t *testing.T, raw string) domain.Version {
	t.Helper()
	v, err := domain.NewVersion(raw)
	if err != nil {
		t.Fatalf("setup version %q: %v", raw, err)
	}
	return v
}

// An entry carries at most one of the two: a semver Version for an artefact the
// owner writes and versions himself, or a numeric Revision counting how many
// times the card for someone else's material was rewritten. Both at once is the
// very defect this split exists to prevent — the live catalog had one field
// holding both meanings, and Brain Fry ended up versioned as "2.0.0" in one
// entry and 5 in another.
func TestNewEntryRejectsVersionAndRevisionTogether(t *testing.T) {
	p := validArticle(t)
	v := mustVersion(t, "1.0.0")
	rev := 1
	p.Version = &v
	p.Revision = &rev

	_, err := domain.NewEntry(p)
	if err == nil {
		t.Fatal("NewEntry accepted both version and revision, want error")
	}
	if !errors.Is(err, domain.ErrInvalidEntry) {
		t.Fatalf("NewEntry error = %v, want ErrInvalidEntry", err)
	}
}

func TestNewEntryAcceptsEitherAloneOrNeither(t *testing.T) {
	v := mustVersion(t, "1.5.1")
	rev := 2
	cases := []struct {
		name     string
		version  *domain.Version
		revision *int
	}{
		{name: "neither"},
		{name: "version alone", version: &v},
		{name: "revision alone", revision: &rev},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validArticle(t)
			p.Version = c.version
			p.Revision = c.revision

			e, err := domain.NewEntry(p)
			if err != nil {
				t.Fatalf("NewEntry: %v", err)
			}
			if c.version == nil && e.Version() != nil {
				t.Fatalf("Version() = %v, want nil", e.Version())
			}
			if c.version != nil && (e.Version() == nil || e.Version().String() != c.version.String()) {
				t.Fatalf("Version() = %v, want %q", e.Version(), c.version.String())
			}
			if c.revision == nil && e.Revision() != nil {
				t.Fatalf("Revision() = %v, want nil", e.Revision())
			}
			if c.revision != nil && (e.Revision() == nil || *e.Revision() != *c.revision) {
				t.Fatalf("Revision() = %v, want %d", e.Revision(), *c.revision)
			}
		})
	}
}

func TestNewEntryRejectsNonPositiveRevision(t *testing.T) {
	// A revision counts editions starting at one. Zero means «no revision», and
	// that is spelled by leaving the field out — storing it as 0 would make the
	// absent case indistinguishable from a real value.
	for _, rev := range []int{0, -1} {
		p := validArticle(t)
		r := rev
		p.Revision = &r

		if _, err := domain.NewEntry(p); err == nil {
			t.Fatalf("NewEntry accepted revision %d, want error", rev)
		}
	}
}

func TestEntryRevisionIsCopied(t *testing.T) {
	// The entity must not alias the caller's int, mirroring how SupersedesID is
	// handled — otherwise a later write through the caller's pointer mutates a
	// constructed entity.
	p := validArticle(t)
	rev := 3
	p.Revision = &rev

	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	rev = 99
	if got := e.Revision(); got == nil || *got != 3 {
		t.Fatalf("Revision() = %v after mutating the source, want 3", got)
	}
}
