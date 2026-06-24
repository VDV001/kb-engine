package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

type fakeQuery struct{}

func (fakeQuery) Stats() (query.Stats, error) {
	return query.Stats{Total: 2, ByCategory: map[string]int{"golang": 2}}, nil
}

func (fakeQuery) Entries() ([]domain.Entry, error) {
	habrID := 1
	rs, _ := domain.NewReadState("read")
	cat, _ := domain.NewCategory("golang")
	lc, _ := domain.NewLifecycle("active")
	v, _ := domain.NewVerdict("keep")
	e, _ := domain.NewEntry(domain.EntryParams{
		ID: 1, Kind: "article", Title: "Hello", Category: cat, Lifecycle: lc,
		HabrID: &habrID, URL: "https://h/x", ReadState: &rs, Verdict: &v,
		Tags: []string{"go"},
	})
	return []domain.Entry{e}, nil
}

type fakeAudit struct{}

func (fakeAudit) OutdatedCandidates() ([]audit.Finding, error) {
	return []audit.Finding{{EntryID: 1, Title: "Hello", Current: "active", Reasons: []string{"keyword:removed"}}}, nil
}
func (fakeAudit) CanonicalCandidates() ([]audit.Finding, error) { return nil, nil }
func (fakeAudit) SupersessionIssues() ([]audit.Finding, error)  { return nil, nil }
func (fakeAudit) Duplicates() ([]audit.DuplicateGroup, error) {
	return []audit.DuplicateGroup{{Kind: "exact-url", Key: "https://h/x", EntryIDs: []int{1, 2}}}, nil
}

type fakeAnalytics struct{}

func (fakeAnalytics) Growth(weeks int) ([]analytics.WeekCount, error) {
	return []analytics.WeekCount{{Week: "17.06", Count: 3}}, nil
}
func (fakeAnalytics) Categories() ([]analytics.CategorySize, error) {
	return []analytics.CategorySize{{Category: "golang", Count: 5}}, nil
}

var testConfig = analyticsconfig.Config{
	Patterns: []analyticsconfig.Pattern{{Name: "Verification > Generation", Desc: "d"}},
	Gaps:     []analyticsconfig.Gap{{Topic: "Testing", Priority: "low"}},
}

func newTestServer() http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, testConfig, nil)
}

func TestServer_analyticsConfig(t *testing.T) {
	rec := get(t, newTestServer(), "/api/analytics-config")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Verification") || !strings.Contains(body, `"Testing"`) {
		t.Errorf("config body missing patterns/gaps: %s", body)
	}
}

func TestServer_analytics(t *testing.T) {
	rec := get(t, newTestServer(), "/api/analytics")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"growth"`) || !strings.Contains(body, `"categories"`) {
		t.Errorf("analytics body missing keys: %s", body)
	}
	if !strings.Contains(body, `"golang"`) {
		t.Errorf("analytics body missing category data: %s", body)
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServer_stats(t *testing.T) {
	rec := get(t, newTestServer(), "/api/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var st query.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Total != 2 {
		t.Errorf("total = %d, want 2", st.Total)
	}
}

func TestServer_entries(t *testing.T) {
	rec := get(t, newTestServer(), "/api/entries")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0]["id"].(float64) != 1 || entries[0]["title"] != "Hello" {
		t.Errorf("entries = %v", entries)
	}
}

func TestServer_audits(t *testing.T) {
	rec := get(t, newTestServer(), "/api/audits")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string][]audit.Finding
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body["outdated"]) != 1 {
		t.Errorf("outdated = %v", body["outdated"])
	}
}

func TestServer_duplicates(t *testing.T) {
	rec := get(t, newTestServer(), "/api/duplicates")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var groups []audit.DuplicateGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(groups) != 1 || len(groups[0].EntryIDs) != 2 {
		t.Errorf("groups = %v", groups)
	}
}

func TestServer_unknownRoute(t *testing.T) {
	rec := get(t, newTestServer(), "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestServer_healthz(t *testing.T) {
	rec := get(t, newTestServer(), "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("healthz body = %q, want it to contain \"ok\"", rec.Body.String())
	}
}

func TestServer_readyz_ok(t *testing.T) {
	rec := get(t, newTestServer(), "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// fakeQueryErr reuses fakeQuery's Entries but fails Stats, modelling a catalog
// that cannot be loaded.
type fakeQueryErr struct{ fakeQuery }

func (fakeQueryErr) Stats() (query.Stats, error) {
	return query.Stats{}, errors.New("catalog unavailable")
}

func TestServer_readyz_unavailable(t *testing.T) {
	srv := httpapi.NewServer(fakeQueryErr{}, fakeAudit{}, fakeAnalytics{}, testConfig, nil)
	rec := get(t, srv, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
