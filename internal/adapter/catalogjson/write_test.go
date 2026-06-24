package catalogjson_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
)

// newArticleEntry builds a fresh unread article entry for append tests.
func newArticleEntry(t *testing.T, id int, title, url string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("unread")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	added := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	e, err := domain.NewEntry(domain.EntryParams{
		ID:        id,
		Kind:      domain.KindArticle,
		Title:     title,
		Category:  cat,
		Lifecycle: lc,
		URL:       url,
		ReadState: &rs,
		Tags:      []string{"go", "tdd"},
		Source:    "bot-inbox",
		Notes:     "Auto-imported",
		DateAdded: &added,
	})
	if err != nil {
		t.Fatalf("new entry: %v", err)
	}
	return e
}

// The existing entry's URL carries an ampersand (utm params) — it must survive
// the rewrite literally, not as &.
const seedCatalog = `{
  "meta": {
    "created": "2026-03-24",
    "description": "тестовый каталог"
  },
  "entries": [
    {
      "id": 5,
      "title": "Существующая",
      "url": "https://habr.com/ru/articles/1/?utm_source=a&utm_medium=rss",
      "category": "golang",
      "status": "KEEP",
      "lifecycle": "active"
    }
  ]
}`

func TestAppendEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(seedCatalog), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	newEntry := newArticleEntry(t, 6, "Новая статья", "https://habr.com/ru/articles/2/")
	if err := catalogjson.AppendEntries(path, []domain.Entry{newEntry}); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}

	// The catalog reloads with both entries intact.
	c, err := catalogjson.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}

	got, ok := c.Find(6)
	if !ok {
		t.Fatal("new entry id=6 not found after append")
	}
	if got.Title() != "Новая статья" {
		t.Errorf("Title = %q", got.Title())
	}
	if got.ReadState() == nil || got.ReadState().String() != "unread" {
		t.Errorf("ReadState = %v, want unread", got.ReadState())
	}
	if got.Verdict() != nil {
		t.Errorf("Verdict = %v, want nil", got.Verdict())
	}
	if got.Source() != "bot-inbox" {
		t.Errorf("Source = %q, want bot-inbox", got.Source())
	}

	// The pre-existing KEEP entry is still there and decodes correctly.
	old, ok := c.Find(5)
	if !ok || old.Verdict() == nil || old.Verdict().String() != "keep" {
		t.Errorf("existing entry 5 not preserved: ok=%v verdict=%v", ok, old.Verdict())
	}

	// Raw-file checks: ampersand preserved literally, meta preserved, file valid.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "utm_source=a&utm_medium=rss") {
		t.Errorf("ampersand URL not preserved literally:\n%s", text)
	}
	if strings.Contains(text, `\u0026`) {
		t.Errorf("ampersand was HTML-escaped to \\u0026")
	}
	if !strings.Contains(text, `"description": "тестовый каталог"`) {
		t.Errorf("meta not preserved:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Errorf("file should end with a trailing newline")
	}
}

// TestAppendEntries_StatusRoundTripsAllKinds backfills coverage for the
// writer's "always loadable" contract: every status aspect a domain-valid entry
// can carry (read-state, verdict, publish-stage) must survive a write→reload.
// This locks the legacyStatus branches so none can ever emit an unloadable
// status field.
func TestAppendEntries_StatusRoundTripsAllKinds(t *testing.T) {
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}

	readState, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	verdict, err := domain.NewVerdict("keep")
	if err != nil {
		t.Fatalf("verdict: %v", err)
	}
	stage, err := domain.NewPublishStage("published")
	if err != nil {
		t.Fatalf("publishstage: %v", err)
	}

	// A read article carrying a verdict (status -> "keep").
	verdictArticle, err := domain.NewEntry(domain.EntryParams{
		ID: 10, Kind: domain.KindArticle, Title: "Reviewed", Category: cat, Lifecycle: lc,
		URL: "https://h/reviewed", ReadState: &readState, Verdict: &verdict,
	})
	if err != nil {
		t.Fatalf("verdict article: %v", err)
	}
	// A creation carrying a publish stage (status -> "published").
	creation, err := domain.NewEntry(domain.EntryParams{
		ID: 11, Kind: domain.KindCreation, Title: "Article draft", Category: cat, Lifecycle: lc,
		PublishStage: &stage,
	})
	if err != nil {
		t.Fatalf("creation: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"meta":{},"entries":[]}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := catalogjson.AppendEntries(path, []domain.Entry{verdictArticle, creation}); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}

	// The crux: the file the writer produced must reload without error.
	c, err := catalogjson.Load(path)
	if err != nil {
		t.Fatalf("writer produced an unloadable file: %v", err)
	}

	got, ok := c.Find(10)
	if !ok || got.Verdict() == nil || got.Verdict().String() != "keep" {
		t.Errorf("verdict article did not round-trip: ok=%v verdict=%v", ok, got.Verdict())
	}
	gotCreation, ok := c.Find(11)
	if !ok || gotCreation.PublishStage() == nil || gotCreation.PublishStage().String() != "published" {
		t.Errorf("creation did not round-trip: ok=%v stage=%v", ok, gotCreation.PublishStage())
	}
}

func TestAppendEntries_RejectsUnknownTopLevelKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"meta":{},"entries":[],"stray":1}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := catalogjson.AppendEntries(path, []domain.Entry{newArticleEntry(t, 1, "X", "u")})
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
}
