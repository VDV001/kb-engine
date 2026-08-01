package drift_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/drift"
)

type stubChecker struct {
	codes     map[string]int
	locations map[string]string
	errs      map[string]error
}

func (s stubChecker) Head(url string) (drift.Response, error) {
	if err, ok := s.errs[url]; ok {
		return drift.Response{}, err
	}
	code, ok := s.codes[url]
	if !ok {
		return drift.Response{}, errors.New("unexpected url " + url)
	}
	return drift.Response{Code: code, Location: s.locations[url]}, nil
}

type fixedLoader struct{ c *domain.Catalog }

func (l fixedLoader) Load() (*domain.Catalog, error) { return l.c, nil }

func entry(t *testing.T, id int, url string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("ai-agents-tools")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: "T", Category: cat,
		Lifecycle: lc, ReadState: &rs, URL: url,
	})
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func catalogOf(t *testing.T, entries ...domain.Entry) *domain.Catalog {
	t.Helper()
	c, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

// The report has to carry what was NOT established, not only what was. A scan
// that reports «2 alive» out of 5 entries, saying nothing about the other 3,
// is how a knowledge base fills up with entries nobody knows are still real.
func TestScan_reportsWhatItCouldNotEstablish(t *testing.T) {
	c := catalogOf(t,
		entry(t, 1, "https://example.com/alive"),
		entry(t, 2, "https://example.com/gone"),
		entry(t, 3, "https://habr.com/ru/articles/1/"),
		entry(t, 4, ""), // no url at all
		entry(t, 5, "https://example.com/unreachable"),
	)
	checker := stubChecker{
		codes: map[string]int{
			"https://example.com/alive":       200,
			"https://example.com/gone":        404,
			"https://habr.com/ru/articles/1/": 403,
		},
		errs: map[string]error{
			"https://example.com/unreachable": errors.New("dial tcp: timeout"),
		},
	}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got := len(rep.Results); got != 3 {
		t.Fatalf("got %d answered urls, want 3 (no url and no answer are not answers)", got)
	}
	if rep.WithoutURL != 1 {
		t.Errorf("WithoutURL = %d, want 1", rep.WithoutURL)
	}
	// Every entry must land in exactly one bucket. This is the property that
	// makes the report honest: if the three numbers do not add up to the
	// catalog size, the scan is silently dropping entries.
	if sum := len(rep.Results) + len(rep.Unreachable) + rep.WithoutURL; sum != rep.TotalEntries {
		t.Fatalf("answered %d + unreachable %d + without url %d = %d, but the catalog has %d entries",
			len(rep.Results), len(rep.Unreachable), rep.WithoutURL, sum, rep.TotalEntries)
	}

	byStatus := map[string]int{}
	for _, r := range rep.Results {
		byStatus[r.Status.String()]++
	}
	for status, want := range map[string]int{"alive": 1, "gone": 1, "undecidable": 1} {
		if byStatus[status] != want {
			t.Errorf("%s = %d, want %d", status, byStatus[status], want)
		}
	}
	if len(rep.Unreachable) != 1 || rep.Unreachable[0].EntryID != 5 {
		t.Errorf("Unreachable = %+v, want one entry 5", rep.Unreachable)
	}
}

// Undecidable results must name themselves loudly enough to reach a summary
// line: this is the number the owner needs to decide whether to open a browser.
func TestReport_undecidableIsCountedSeparately(t *testing.T) {
	c := catalogOf(t,
		entry(t, 1, "https://habr.com/a/"),
		entry(t, 2, "https://habr.com/b/"),
		entry(t, 3, "https://example.com/ok"),
	)
	checker := stubChecker{codes: map[string]int{
		"https://habr.com/a/":    403,
		"https://habr.com/b/":    403,
		"https://example.com/ok": 200,
	}}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := rep.Undecidable(); got != 2 {
		t.Fatalf("Undecidable() = %d, want 2", got)
	}
	if got := rep.Actionable(); len(got) != 0 {
		t.Fatalf("Actionable() = %+v, want none — a 403 is not a verdict", got)
	}
}

// A scan is a claim about a moment. Without the timestamp on every result the
// catalog cannot tell a fresh check from one made in May.
func TestScan_stampsEveryResult(t *testing.T) {
	when := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	c := catalogOf(t, entry(t, 1, "https://example.com/x"))
	checker := stubChecker{codes: map[string]int{"https://example.com/x": 200}}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(when)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !rep.Results[0].CheckedAt.Equal(when) {
		t.Fatalf("CheckedAt = %v, want %v", rep.Results[0].CheckedAt, when)
	}
}

// A full scan of the live catalog is 1313 requests at half a second each. The
// limit exists so a first run can be a sample — and the report must then say
// the coverage is partial, not look like a complete scan.
func TestScan_limitStopsEarlyAndSaysSo(t *testing.T) {
	c := catalogOf(t,
		entry(t, 1, "https://example.com/a"),
		entry(t, 2, "https://example.com/b"),
		entry(t, 3, "https://example.com/c"),
	)
	checker := stubChecker{codes: map[string]int{
		"https://example.com/a": 200,
		"https://example.com/b": 200,
		"https://example.com/c": 200,
	}}

	svc := drift.NewService(fixedLoader{c}, checker)
	svc.Limit = 2

	rep, err := svc.Scan(time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("checked %d urls, want 2", len(rep.Results))
	}
	if rep.NotAttempted != 1 {
		t.Fatalf("NotAttempted = %d, want 1 — a partial scan must not read as a complete one", rep.NotAttempted)
	}
	if sum := len(rep.Results) + len(rep.Unreachable) + rep.WithoutURL + rep.NotAttempted; sum != rep.TotalEntries {
		t.Fatalf("buckets sum to %d, catalog has %d", sum, rep.TotalEntries)
	}
}

// Habr moved 179 of the catalog's addresses: a company renamed itself, articles
// crossed between sections. The old address still redirects, so the link works
// — but the catalog stores an address that is no longer the canonical one, and
// the day habr drops those redirects all 179 die at once.
func TestScan_carriesTheRedirectTarget(t *testing.T) {
	c := catalogOf(t, entry(t, 51, "https://habr.com/ru/companies/pgk/articles/1013700/"))
	checker := stubChecker{
		codes:     map[string]int{"https://habr.com/ru/companies/pgk/articles/1013700/": 302},
		locations: map[string]string{"https://habr.com/ru/companies/pgk/articles/1013700/": "https://habr.com/ru/companies/pgkdigital/articles/1013700/"},
	}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := rep.Results[0].Location; got != "https://habr.com/ru/companies/pgkdigital/articles/1013700/" {
		t.Fatalf("Location = %q, want the redirect target", got)
	}
	moved := rep.Moved()
	if len(moved) != 1 || moved[0].EntryID != 51 {
		t.Fatalf("Moved() = %+v, want the one redirected entry", moved)
	}
}

// A redirect without a target tells the owner nothing to act on, so it must not
// appear in the list of addresses to update.
func TestReport_movedSkipsRedirectsWithoutATarget(t *testing.T) {
	c := catalogOf(t, entry(t, 1, "https://example.com/x"))
	checker := stubChecker{codes: map[string]int{"https://example.com/x": 302}}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := rep.Moved(); len(got) != 0 {
		t.Fatalf("Moved() = %+v, want none — there is no new address to write", got)
	}
}
