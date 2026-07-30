package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
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
	added := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	e, _ := domain.NewEntry(domain.EntryParams{
		ID: 1, Kind: "article", Title: "Hello", Category: cat, Lifecycle: lc,
		HabrID: &habrID, URL: "https://h/x", ReadState: &rs, Verdict: &v,
		Tags: []string{"go"}, DateAdded: &added,
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
func (fakeAnalytics) Graph() (analytics.Graph, error) {
	return analytics.Graph{
		Nodes: []analytics.GraphNode{{Category: "golang", Count: 5}},
		Edges: []analytics.GraphEdge{{From: "golang", To: "meta", Weight: 2}},
	}, nil
}

var testConfig = analyticsconfig.Config{
	Patterns: []analyticsconfig.Pattern{{Name: "Verification > Generation", Desc: "d"}},
	Gaps:     []analyticsconfig.Gap{{Topic: "Testing", Priority: "low"}},
}

type fakeFinance struct{ err error }

func (f fakeFinance) Finances() (httpapi.Finances, error) {
	if f.err != nil {
		return httpapi.Finances{}, f.err
	}
	now := func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) }
	amount, _ := domain.ParseMoney("500.00")
	tx, _ := domain.NewTransaction(domain.TransactionParams{
		ID: "01ABC", Kind: "expense", Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Amount: amount, Category: "Еда", Subcategory: "Продукты", Place: "Лавка",
		Account: "Сбербанк", Source: "Чек", Now: now,
	})
	balance, _ := domain.ParseMoney("1000.00")
	acc, _ := domain.NewAccount("Сбербанк", balance, time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), now)
	return httpapi.Finances{
		Transactions: []domain.Transaction{tx},
		Accounts:     []domain.Account{acc},
	}, nil
}

func newTestServer() http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) {
			return changelog.Document{CurrentVersion: "0.9.0"}, nil
		}, nil)
}

// The dashboard needs the rows and the balances; it does the filtering by month
// and the totals itself, the same way the entries view already filters entries.
// Money crosses as a decimal string, not a float — the ledger is kopecks, and a
// float would put 89.98999999999999 on screen.
func TestServer_finances(t *testing.T) {
	rec := get(t, newTestServer(), "/api/finances")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Accounts     []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Transactions) != 1 || len(body.Accounts) != 1 {
		t.Fatalf("got %d transactions and %d accounts, want 1 and 1",
			len(body.Transactions), len(body.Accounts))
	}
	tx := body.Transactions[0]
	for field, want := range map[string]any{
		"kind": "expense", "date": "2026-07-01", "amount": "500.00",
		"category": "Еда", "account": "Сбербанк",
	} {
		if tx[field] != want {
			t.Errorf("transaction[%q] = %v, want %v", field, tx[field], want)
		}
	}
	if acc := body.Accounts[0]; acc["bank"] != "Сбербанк" || acc["balance"] != "1000.00" {
		t.Errorf("account = %v", acc)
	}
}

// Finances are optional: a deployment with no ledger configured still serves the
// rest of the dashboard, and the view says there is nothing rather than breaking.
func TestServer_finances_notConfigured(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, nil,
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, nil)
	rec := get(t, srv, "/api/finances")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Accounts     []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Transactions) != 0 || len(body.Accounts) != 0 {
		t.Errorf("body = %+v, want both empty", body)
	}
}

func TestServer_finances_error(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{},
		fakeFinance{err: errors.New("ledger unreadable")},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, nil)
	if rec := get(t, srv, "/api/finances"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
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
	// The catalog view sorts and displays by the date an entry joined the
	// catalog; without it every row reads "—" and sorting is by id pretending
	// to be chronology.
	if entries[0]["date_added"] != "2026-07-11" {
		t.Errorf("date_added = %v, want 2026-07-11", entries[0]["date_added"])
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
	srv := httpapi.NewServer(fakeQueryErr{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, nil)
	rec := get(t, srv, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// Settings' «Что нового» reads the changelog through the API rather than a
// baked copy: the loader is called per request, so a released version shows up
// on the next reload like every other data source here.
func TestServer_changelog(t *testing.T) {
	rec := get(t, newTestServer(), "/api/changelog")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc["current_version"] != "0.9.0" {
		t.Errorf("current_version = %v, want 0.9.0", doc["current_version"])
	}
}
