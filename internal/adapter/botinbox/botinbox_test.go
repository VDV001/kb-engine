package botinbox_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/botinbox"
	"github.com/daniil/kb-engine/internal/domain"
)

func TestDecodeArticles(t *testing.T) {
	t.Run("array of articles", func(t *testing.T) {
		const in = `[
			{"title":"A","url":"https://h/1","hub":"go","author":"x","tags":["t1"],"createdAt":"2026-05-19T07:01:07.000Z"},
			{"title":"B","url":"https://h/2","hub":"devops","author":"y","tags":[],"createdAt":"2026-05-20T00:00:00.000Z"}
		]`
		got, err := botinbox.DecodeArticles(strings.NewReader(in))
		if err != nil {
			t.Fatalf("DecodeArticles: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Title != "A" || got[1].Hub != "devops" {
			t.Errorf("unexpected decode: %+v", got)
		}
	})

	t.Run("single object is wrapped", func(t *testing.T) {
		const in = `{"title":"Solo","url":"https://h/9","hub":"go","tags":["x"],"createdAt":"2026-05-19T07:01:07.000Z"}`
		got, err := botinbox.DecodeArticles(strings.NewReader(in))
		if err != nil {
			t.Fatalf("DecodeArticles: %v", err)
		}
		if len(got) != 1 || got[0].Title != "Solo" {
			t.Fatalf("got %+v, want one article 'Solo'", got)
		}
	})

	t.Run("malformed json errors", func(t *testing.T) {
		if _, err := botinbox.DecodeArticles(strings.NewReader("not json")); err == nil {
			t.Fatal("expected error for malformed json")
		}
	})
}

func TestMapArticle(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	t.Run("known hub maps category, cleans tags, builds entry params", func(t *testing.T) {
		a := botinbox.Article{
			Title:     "Как React обновляет UI",
			URL:       "https://habr.com/ru/articles/1016180/",
			Hub:       "reactjs",
			Author:    "someone",
			Tags:      []string{"React Fiber", "internals", "reactjs"},
			CreatedAt: "2026-03-28T09:30:00.000Z",
		}
		p, err := botinbox.MapArticle(a, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}

		if p.Kind != domain.KindArticle {
			t.Errorf("Kind = %q, want %q", p.Kind, domain.KindArticle)
		}
		if p.Category.String() != "frontend" {
			t.Errorf("Category = %q, want frontend", p.Category.String())
		}
		if p.Lifecycle.String() != "active" {
			t.Errorf("Lifecycle = %q, want active", p.Lifecycle.String())
		}
		if p.ReadState == nil || p.ReadState.String() != "unread" {
			t.Errorf("ReadState = %v, want unread", p.ReadState)
		}
		if p.Verdict != nil {
			t.Errorf("Verdict = %v, want nil for fresh import", p.Verdict)
		}
		if p.Source != "bot-inbox" {
			t.Errorf("Source = %q, want bot-inbox", p.Source)
		}
		// hub is prepended, then article tags; deduped; lowercased; spaces→hyphen.
		want := []string{"reactjs", "react-fiber", "internals"}
		if strings.Join(p.Tags, ",") != strings.Join(want, ",") {
			t.Errorf("Tags = %v, want %v", p.Tags, want)
		}
		if p.URL != a.URL {
			t.Errorf("URL = %q, want %q", p.URL, a.URL)
		}
		if p.DateCreated == nil || p.DateCreated.Format("2006-01-02") != "2026-03-28" {
			t.Errorf("DateCreated = %v, want 2026-03-28", p.DateCreated)
		}
		if !strings.Contains(p.Notes, "someone") {
			t.Errorf("Notes = %q, want author reference", p.Notes)
		}
		// The resulting params must build a valid entry once an id is assigned.
		p.ID = 1
		if _, err := domain.NewEntry(p); err != nil {
			t.Errorf("NewEntry from mapped params: %v", err)
		}
	})

	t.Run("unknown hub falls back to dev-practices", func(t *testing.T) {
		p, err := botinbox.MapArticle(botinbox.Article{
			Title: "T", URL: "u", Hub: "totally_unknown_hub", CreatedAt: "2026-01-01T00:00:00.000Z",
		}, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}
		if p.Category.String() != "dev-practices" {
			t.Errorf("Category = %q, want dev-practices", p.Category.String())
		}
	})

	t.Run("empty title falls back to Untitled", func(t *testing.T) {
		p, err := botinbox.MapArticle(botinbox.Article{
			URL: "u", Hub: "go", CreatedAt: "2026-01-01T00:00:00.000Z",
		}, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}
		if p.Title != "Untitled" {
			t.Errorf("Title = %q, want Untitled", p.Title)
		}
	})

	t.Run("missing createdAt uses now", func(t *testing.T) {
		p, err := botinbox.MapArticle(botinbox.Article{Title: "T", URL: "u", Hub: "go"}, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}
		if p.DateCreated == nil || p.DateCreated.Format("2006-01-02") != "2026-06-24" {
			t.Errorf("DateCreated = %v, want now 2026-06-24", p.DateCreated)
		}
	})

	t.Run("description truncated to 200 runes", func(t *testing.T) {
		long := strings.Repeat("я", 250) // multibyte runes
		p, err := botinbox.MapArticle(botinbox.Article{
			Title: "T", URL: "u", Hub: "go", Description: long, CreatedAt: "2026-01-01T00:00:00.000Z",
		}, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}
		if n := len([]rune(p.Description)); n != 200 {
			t.Errorf("Description rune len = %d, want 200", n)
		}
	})

	t.Run("tags capped at 8", func(t *testing.T) {
		p, err := botinbox.MapArticle(botinbox.Article{
			Title: "T", URL: "u", Hub: "go",
			Tags:      []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
			CreatedAt: "2026-01-01T00:00:00.000Z",
		}, now)
		if err != nil {
			t.Fatalf("MapArticle: %v", err)
		}
		if len(p.Tags) != 8 {
			t.Errorf("len(Tags) = %d, want 8", len(p.Tags))
		}
	})
}
