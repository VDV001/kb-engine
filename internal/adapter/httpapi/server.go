// Package httpapi exposes the catalog query and audit use cases over HTTP as
// JSON, and optionally serves an embedded frontend. It is a delivery adapter:
// it maps domain objects to JSON DTOs and delegates all logic to use cases.
package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// growthWeeks is how many weeks of growth history the analytics endpoint serves.
const growthWeeks = 12

// Querier is the read-query port the API depends on.
type Querier interface {
	Stats() (query.Stats, error)
	Entries() ([]domain.Entry, error)
}

// Auditor is the audit port the API depends on.
type Auditor interface {
	OutdatedCandidates() ([]audit.Finding, error)
	CanonicalCandidates() ([]audit.Finding, error)
	SupersessionIssues() ([]audit.Finding, error)
	Duplicates() ([]audit.DuplicateGroup, error)
}

// Analyzer is the analytics port the API depends on. It owns its own clock, so
// the handler never reads the wall clock.
type Analyzer interface {
	Growth(weeks int) ([]analytics.WeekCount, error)
	Categories() ([]analytics.CategorySize, error)
}

// Finances is what the finance port hands over: the ledger rows and the account
// balances. Aggregation is deliberately not here — the view filters by month
// and totals what it filtered, so the arithmetic lives in one place instead of
// being split between a server-side summary and a client-side one that must
// agree with it.
type Finances struct {
	Transactions []domain.Transaction
	Accounts     []domain.Account
}

// Financier is the finance port the API depends on. A nil Financier means no
// ledger is configured, which is a valid deployment: the rest of the dashboard
// works and the Finances view shows nothing.
type Financier interface {
	Finances() (Finances, error)
}

// NewServer builds the HTTP handler. cfg is the curated analytics config (empty
// when none is configured). fin may be nil when no ledger is configured. If
// frontend is non-nil its files are served at the root (with index.html
// fallback for client-side routes).
func NewServer(q Querier, a Auditor, an Analyzer, fin Financier, cfg analyticsconfig.Config, frontend fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(q))
	mux.HandleFunc("GET /api/stats", handleStats(q))
	mux.HandleFunc("GET /api/entries", handleEntries(q))
	mux.HandleFunc("GET /api/audits", handleAudits(a))
	mux.HandleFunc("GET /api/duplicates", handleDuplicates(a))
	mux.HandleFunc("GET /api/analytics", handleAnalytics(an))
	mux.HandleFunc("GET /api/analytics-config", handleAnalyticsConfig(cfg))
	mux.HandleFunc("GET /api/finances", handleFinances(fin))
	if frontend != nil {
		mux.Handle("/", spaHandler(frontend))
	}
	return mux
}

// handleHealthz is a liveness probe: it returns 200 as long as the process can
// serve requests. It does no I/O.
func handleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	}
}

// handleReadyz is a readiness probe: it returns 200 only when the catalog can
// be loaded, and 503 otherwise, so an orchestrator holds traffic until the data
// source is reachable.
func handleReadyz(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if _, err := q.Stats(); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ready\n"))
	}
}

func handleAnalyticsConfig(cfg analyticsconfig.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, cfg)
	}
}

func handleAnalytics(an Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		growth, err := an.Growth(growthWeeks)
		if err != nil {
			writeError(w, err)
			return
		}
		categories, err := an.Categories()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"growth":     growth,
			"categories": categories,
		})
	}
}

func handleStats(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		st, err := q.Stats()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, st)
	}
}

func handleEntries(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		entries, err := q.Entries()
		if err != nil {
			writeError(w, err)
			return
		}
		dtos := make([]entryDTO, 0, len(entries))
		for _, e := range entries {
			dtos = append(dtos, toDTO(e))
		}
		writeJSON(w, dtos)
	}
}

func handleAudits(a Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		outdated, err := a.OutdatedCandidates()
		if err != nil {
			writeError(w, err)
			return
		}
		canonical, err := a.CanonicalCandidates()
		if err != nil {
			writeError(w, err)
			return
		}
		supersession, err := a.SupersessionIssues()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string][]audit.Finding{
			"outdated":     outdated,
			"canonical":    canonical,
			"supersession": supersession,
		})
	}
}

func handleFinances(fin Financier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Empty rather than 404: the view asks unconditionally, and "no ledger
		// configured" is a shape it can render, not an error it has to handle.
		txs, accounts := []transactionDTO{}, []accountDTO{}
		if fin != nil {
			f, err := fin.Finances()
			if err != nil {
				writeError(w, err)
				return
			}
			for _, t := range f.Transactions {
				txs = append(txs, toTransactionDTO(t))
			}
			for _, a := range f.Accounts {
				accounts = append(accounts, toAccountDTO(a))
			}
		}
		writeJSON(w, map[string]any{"transactions": txs, "accounts": accounts})
	}
}

func handleDuplicates(a Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		groups, err := a.Duplicates()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, groups)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
