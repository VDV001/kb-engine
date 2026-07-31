package domain_test

import (
	"errors"
	"testing"
	"time"

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
		ID:          1,
		Kind:        "article",
		Title:       "Some article",
		Category:    mustCategory(t),
		Lifecycle:   mustLifecycle(t),
		HabrID:      &habrID,
		URL:         "https://habr.com/ru/articles/1049782/",
		ReadState:   &rs,
		Verdict:     &v,
		Tags:        []string{"agent-security", "mcp"},
		Description: "prod-debug via narrow read-only MCP",
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
	t.Run("article preserves every field", func(t *testing.T) {
		e, err := domain.NewEntry(validArticle(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.ID() != 1 || e.Kind() != "article" || e.Title() != "Some article" {
			t.Errorf("unexpected entry: id=%d kind=%q title=%q", e.ID(), e.Kind(), e.Title())
		}
		if e.Category().String() != "ai-agents-tools" {
			t.Errorf("category = %q, want ai-agents-tools", e.Category().String())
		}
		if e.Lifecycle().String() != "active" {
			t.Errorf("lifecycle = %q, want active", e.Lifecycle().String())
		}
		if e.HabrID() == nil || *e.HabrID() != 1049782 {
			t.Errorf("habrID = %v, want 1049782", e.HabrID())
		}
		if e.URL() != "https://habr.com/ru/articles/1049782/" {
			t.Errorf("url = %q", e.URL())
		}
		if e.ReadState() == nil || e.ReadState().String() != "read" {
			t.Errorf("read state = %v, want read", e.ReadState())
		}
		if e.Verdict() == nil || e.Verdict().String() != "keep" {
			t.Errorf("verdict = %v, want keep", e.Verdict())
		}
		if got := e.Tags(); len(got) != 2 || got[0] != "agent-security" || got[1] != "mcp" {
			t.Errorf("tags = %v, want [agent-security mcp]", got)
		}
		if e.Description() != "prod-debug via narrow read-only MCP" {
			t.Errorf("description = %q", e.Description())
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

	t.Run("minimal article without habr_id or url is allowed", func(t *testing.T) {
		// Real data: bot-inbox links are articles with neither a parsed habr_id
		// nor always a url. The only article invariant is a read-state.
		p := validArticle(t)
		p.HabrID = nil
		p.URL = ""
		e, err := domain.NewEntry(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.HabrID() != nil {
			t.Errorf("habrID = %v, want nil", e.HabrID())
		}
		if e.URL() != "" {
			t.Errorf("url = %q, want empty", e.URL())
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

func TestNewEntry_metadata(t *testing.T) {
	p := validArticle(t)
	sup := 5
	added := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.Source = "bot-inbox"
	p.Author = "Some Author"
	p.Notes = "a note"
	p.SupersedesID = &sup
	p.RelatedIDs = []int{2, 3}
	p.DateAdded = &added
	p.DateCreated = &created

	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Source() != "bot-inbox" || e.Author() != "Some Author" || e.Notes() != "a note" {
		t.Errorf("metadata mismatch: source=%q author=%q notes=%q", e.Source(), e.Author(), e.Notes())
	}
	if e.SupersedesID() == nil || *e.SupersedesID() != 5 {
		t.Errorf("supersedesID = %v, want 5", e.SupersedesID())
	}
	if got := e.RelatedIDs(); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("relatedIDs = %v, want [2 3]", got)
	}
	if e.DateAdded() == nil || !e.DateAdded().Equal(added) {
		t.Errorf("dateAdded = %v, want %v", e.DateAdded(), added)
	}
	if e.DateCreated() == nil || !e.DateCreated().Equal(created) {
		t.Errorf("dateCreated = %v, want %v", e.DateCreated(), created)
	}
}

func TestNewEntry_relatedIDsAreImmutable(t *testing.T) {
	p := validArticle(t)
	src := []int{2, 3}
	p.RelatedIDs = src

	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	src[0] = 999
	if got := e.RelatedIDs(); got[0] != 2 {
		t.Errorf("related id changed via source slice: %v", got)
	}
	e.RelatedIDs()[1] = 999
	if got := e.RelatedIDs(); got[1] != 3 {
		t.Errorf("related id changed via returned slice: %v", got)
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

// Признак перевода жил префиксом «[Перевод]» внутри заголовка — метаданные
// в строке контента. Шестьдесят записей каталога несли его так, и при
// переводе интерфейса это слово осталось бы русским: оно лежало не в поле,
// а в тексте. Теперь это поле, и заголовок снова только заголовок.
func TestEntry_isTranslation(t *testing.T) {
	p := validArticle(t)
	p.IsTranslation = true
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("new entry: %v", err)
	}
	if !e.IsTranslation() {
		t.Error("IsTranslation = false, want true")
	}

	p2 := validArticle(t)
	e2, err := domain.NewEntry(p2)
	if err != nil {
		t.Fatalf("new entry: %v", err)
	}
	// Умолчание — «не перевод»: так записано у 1280 записей из 1340, и
	// отсутствие поля обязано читаться именно так, а не как «неизвестно».
	if e2.IsTranslation() {
		t.Error("IsTranslation = true for an entry without the field, want false")
	}
}
