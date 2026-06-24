package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// helpers build valid VOs so each test case can mutate one thing at a time.

func mustCategory(t *testing.T) domain.Category {
	t.Helper()
	c, err := domain.NewCategory("ai-agents-tools")
	if err != nil {
		t.Fatalf("setup category: %v", err)
	}
	return c
}

func mustLifecycle(t *testing.T) domain.Lifecycle {
	t.Helper()
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("setup lifecycle: %v", err)
	}
	return lc
}

func mustVerdict(t *testing.T) domain.Verdict {
	t.Helper()
	v, err := domain.NewVerdict("keep")
	if err != nil {
		t.Fatalf("setup verdict: %v", err)
	}
	return v
}

func mustPublishStage(t *testing.T) domain.PublishStage {
	t.Helper()
	ps, err := domain.NewPublishStage("published")
	if err != nil {
		t.Fatalf("setup publish stage: %v", err)
	}
	return ps
}

func mustReadState(t *testing.T) domain.ReadState {
	t.Helper()
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("setup read state: %v", err)
	}
	return rs
}

func validArticle(t *testing.T) domain.EntryParams {
	t.Helper()
	habrID := 1049782
	v := mustVerdict(t)
	rs := mustReadState(t)
	return domain.EntryParams{
		ID:        1,
		Kind:      "article",
		Title:     "Some article",
		Category:  mustCategory(t),
		Lifecycle: mustLifecycle(t),
		HabrID:    &habrID,
		URL:       "https://habr.com/ru/articles/1049782/",
		ReadState: &rs,
		Verdict:   &v,
	}
}

func validCreation(t *testing.T) domain.EntryParams {
	t.Helper()
	ps := mustPublishStage(t)
	return domain.EntryParams{
		ID:           2,
		Kind:         "creation",
		Title:        "My research",
		Category:     mustCategory(t),
		Lifecycle:    mustLifecycle(t),
		PublishStage: &ps,
	}
}

func TestNewEntry_valid(t *testing.T) {
	t.Run("article", func(t *testing.T) {
		e, err := domain.NewEntry(validArticle(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.ID() != 1 || e.Kind() != "article" || e.Title() != "Some article" {
			t.Errorf("unexpected entry: id=%d kind=%q title=%q", e.ID(), e.Kind(), e.Title())
		}
		if e.Verdict() == nil || e.Verdict().String() != "keep" {
			t.Errorf("verdict = %v, want keep", e.Verdict())
		}
	})

	t.Run("creation", func(t *testing.T) {
		e, err := domain.NewEntry(validCreation(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.PublishStage() == nil || e.PublishStage().String() != "published" {
			t.Errorf("publish stage = %v, want published", e.PublishStage())
		}
	})

	t.Run("article without verdict is allowed (read but not yet decided)", func(t *testing.T) {
		p := validArticle(t)
		p.Verdict = nil
		e, err := domain.NewEntry(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Verdict() != nil {
			t.Errorf("verdict = %v, want nil", e.Verdict())
		}
		if e.ReadState() == nil || e.ReadState().String() != "read" {
			t.Errorf("read state = %v, want read", e.ReadState())
		}
	})
}

func TestNewEntry_invalid(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(p *domain.EntryParams)
		wantErr error
	}{
		{"non-positive id", func(p *domain.EntryParams) { p.ID = 0 }, domain.ErrInvalidEntry},
		{"empty title", func(p *domain.EntryParams) { p.Title = "  " }, domain.ErrInvalidEntry},
		{"unknown kind", func(p *domain.EntryParams) { p.Kind = "bogus" }, domain.ErrUnknownKind},
		{"article without url", func(p *domain.EntryParams) { p.URL = "" }, domain.ErrInvalidEntry},
		{"article without habr id", func(p *domain.EntryParams) { p.HabrID = nil }, domain.ErrInvalidEntry},
		{"article without read state", func(p *domain.EntryParams) { p.ReadState = nil }, domain.ErrInvalidEntry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validArticle(t)
			tt.mutate(&p)
			_, err := domain.NewEntry(p)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewEntry_creationRequiresPublishStage(t *testing.T) {
	p := validCreation(t)
	p.PublishStage = nil
	_, err := domain.NewEntry(p)
	if !errors.Is(err, domain.ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntry_tagsAreImmutable(t *testing.T) {
	p := validArticle(t)
	src := []string{"go", "ddd"}
	p.Tags = src

	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutating the caller's slice must not reach into the entry.
	src[0] = "MUTATED"
	if got := e.Tags(); got[0] != "go" {
		t.Errorf("entry tag changed via source slice: %v", got)
	}

	// Mutating the returned slice must not reach into the entry either.
	e.Tags()[1] = "MUTATED"
	if got := e.Tags(); got[1] != "ddd" {
		t.Errorf("entry tag changed via returned slice: %v", got)
	}
}
