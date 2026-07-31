// Package httpapi exposes the catalog query and audit use cases over HTTP as
// JSON, and optionally serves an embedded frontend. It is a delivery adapter:
// it maps domain objects to JSON DTOs and delegates all logic to use cases.
package httpapi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// growthWeeks is how many weeks of growth history the analytics endpoint serves.
const growthWeeks = 12

// Querier is the read-query port the API depends on.
type Querier interface {
	Stats() (query.Stats, error)
	Entries() ([]domain.Entry, error)
	Health() (query.Health, error)
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
	Graph() (analytics.Graph, error)
}

// ConfigLoader supplies the curated analytics config. Called per request, so
// an edited analytics_config.json shows up on the next reload without
// restarting the engine — the same liveness the catalog already has.
type ConfigLoader func() (analyticsconfig.Config, error)

// ChangelogLoader supplies the parsed changelog, nil when none is configured.
// Also called per request, for the same reason.
type ChangelogLoader func() (changelog.Document, error)

// Documents are the owner's personal views — Now, Team, Projects — served from
// files the engine is pointed at. The repo carries only the renderer: an
// AGPL-public engine must never embed anyone's team or projects. Each loader
// is optional and called per request; nil means the view is not configured,
// which is a smaller KB, not an error.
type Documents struct {
	// Now returns markdown (the active pipeline document).
	Now func() (string, error)
	// Team and Projects return the owner's JSON verbatim — the engine does
	// not reshape content it does not own.
	Team     func() ([]byte, error)
	Projects func() ([]byte, error)
	// Media is the owner's image directory, served under /media/. Screenshots
	// referenced from projects.json live there rather than in the bundle, for
	// the same reason the JSON does: they are his content, not the engine's.
	Media fs.FS
}

// Finances is what the finance port hands over: the ledger rows and the account
// balances, unaggregated. The journal needs the rows themselves — it lists,
// filters and sorts them — so this endpoint stays.
type Finances struct {
	Transactions []domain.Transaction
	Accounts     []domain.Account
}

// Financier is the finance port the API depends on. A nil Financier means no
// ledger is configured, which is a valid deployment: the rest of the dashboard
// works and the Finances view shows nothing.
//
// Summary takes the period rather than returning everything and letting the
// client narrow it. That ordering is the whole point: the arithmetic still lives
// in exactly one place, and now that place is the server. Returning a
// full-history summary and having the view re-total a filtered subset would put
// a second implementation on the client that has to agree with this one — which
// is the split the earlier design avoided by keeping all of it client-side.
type Financier interface {
	Finances() (Finances, error)
	Summary(months []string) (finance.Summary, error)
}

// NewServer builds the HTTP handler. cfg is the curated analytics config (empty
// when none is configured). fin may be nil when no ledger is configured. If
// frontend is non-nil its files are served at the root (with index.html
// fallback for client-side routes).
func NewServer(q Querier, a Auditor, an Analyzer, fin Financier, cfg ConfigLoader, chlog ChangelogLoader, docs Documents, frontend fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(q))
	mux.HandleFunc("GET /api/stats", handleStats(q))
	mux.HandleFunc("GET /api/entries", handleEntries(q))
	mux.HandleFunc("GET /api/audits", handleAudits(a))
	mux.HandleFunc("GET /api/duplicates", handleDuplicates(a))
	mux.HandleFunc("GET /api/analytics", handleAnalytics(an))
	mux.HandleFunc("GET /api/analytics-config", handleAnalyticsConfig(cfg))
	mux.HandleFunc("GET /api/graph", handleGraph(an))
	mux.HandleFunc("GET /api/changelog", handleChangelog(chlog))
	mux.HandleFunc("GET /api/now", handleNow(docs.Now))
	mux.HandleFunc("GET /api/team", handleRawJSON(docs.Team))
	mux.HandleFunc("GET /api/projects", handleRawJSON(docs.Projects))
	mux.HandleFunc("GET /api/finances", handleFinances(fin))
	mux.HandleFunc("GET /api/finances/summary", handleFinanceSummary(fin))
	if docs.Media != nil {
		mux.Handle("GET /media/", http.StripPrefix("/media/", mediaHandler(docs.Media)))
	}
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

func handleAnalyticsConfig(cfg ConfigLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		c, err := cfg()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, c)
	}
}

func handleNow(load func() (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if load == nil {
			writeJSON(w, nil)
			return
		}
		md, err := load()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]string{"markdown": md})
	}
}

// handleRawJSON serves an owner-supplied JSON file byte-for-byte.
func handleRawJSON(load func() ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if load == nil {
			writeJSON(w, nil)
			return
		}
		raw, err := load()
		if err != nil {
			writeError(w, err)
			return
		}
		if !json.Valid(raw) {
			// Отдать битый файл — значит сломать view молча; ошибка честнее.
			writeError(w, fmt.Errorf("document is not valid JSON"))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(raw)
	}
}

func handleChangelog(chlog ChangelogLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if chlog == nil {
			// Не настроен — валидное развёртывание: view покажет пусто.
			writeJSON(w, changelog.Document{})
			return
		}
		doc, err := chlog()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, doc)
	}
}

func handleGraph(an Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		g, err := an.Graph()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, g)
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
		// Здоровье едет вместе со статистикой, а не отдельным запросом: это
		// такой же агрегат по тому же каталогу, и рисуются они на одном экране.
		h, err := q.Health()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, struct {
			query.Stats
			Health query.Health `json:"health"`
		}{st, h})
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
				// The message carries the path to a personal finance file, which is
				// not something to hand to whoever asked. The operator gets it on
				// stderr; the client gets that it failed.
				log.Printf("finances: %v", err)
				http.Error(w, "finances unavailable", http.StatusInternalServerError)
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

// parseMonths splits the ?months= parameter into a set of YYYY-MM keys.
//
// Empty elements are dropped rather than passed on. «2026-07,» would otherwise
// become a set containing an empty string, which matches no record at all, and
// the caller would get an empty report for a month that has data — a wrong
// answer that looks like a legitimate one.
func parseMonths(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if m := strings.TrimSpace(part); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func handleFinanceSummary(fin Financier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s finance.Summary
		if fin != nil {
			var err error
			if s, err = fin.Summary(parseMonths(r.URL.Query().Get("months"))); err != nil {
				// Same reasoning as handleFinances: the message names a personal
				// finance file. The operator gets the path, the client gets that it
				// failed.
				log.Printf("finances summary: %v", err)
				http.Error(w, "finances unavailable", http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, toFinanceSummaryDTO(s))
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
