// Package httpapi exposes the catalog query and audit use cases over HTTP as
// JSON, and optionally serves an embedded frontend. It is a delivery adapter:
// it maps domain objects to JSON DTOs and delegates all logic to use cases.
package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

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

// Analyzer is the analytics port the API depends on.
type Analyzer interface {
	Growth(now time.Time, weeks int) ([]analytics.WeekCount, error)
	Categories() ([]analytics.CategorySize, error)
}

// NewServer builds the HTTP handler. cfg is the curated analytics config (empty
// when none is configured). If frontend is non-nil its files are served at the
// root (with index.html fallback for client-side routes).
func NewServer(q Querier, a Auditor, an Analyzer, cfg analyticsconfig.Config, frontend fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats", handleStats(q))
	mux.HandleFunc("GET /api/entries", handleEntries(q))
	mux.HandleFunc("GET /api/audits", handleAudits(a))
	mux.HandleFunc("GET /api/duplicates", handleDuplicates(a))
	mux.HandleFunc("GET /api/analytics", handleAnalytics(an))
	mux.HandleFunc("GET /api/analytics-config", handleAnalyticsConfig(cfg))
	if frontend != nil {
		mux.Handle("/", spaHandler(frontend))
	}
	return mux
}

func handleAnalyticsConfig(cfg analyticsconfig.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, cfg)
	}
}

func handleAnalytics(an Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		growth, err := an.Growth(time.Now(), growthWeeks)
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
