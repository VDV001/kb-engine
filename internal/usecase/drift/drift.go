// Package drift checks whether the URLs in the catalog still resolve.
//
// Its contract is unusual and deliberate: the report carries what the scan
// could NOT establish as prominently as what it could. A knowledge base fills
// up with stale entries not because checks fail, but because they quietly
// cover less than the reader assumes.
package drift

import (
	"fmt"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// CatalogLoader is the port for obtaining the catalog (DIP: declared with its
// consumer).
type CatalogLoader interface {
	Load() (*domain.Catalog, error)
}

// LinkChecker asks one URL for its status code. Implementations do network I/O.
type LinkChecker interface {
	Head(url string) (int, error)
}

// Result is what the scan learned about one entry's URL.
type Result struct {
	EntryID   int
	Title     string
	URL       string
	Code      int
	Status    domain.LinkStatus
	CheckedAt time.Time
}

// Unreachable is an entry whose URL could not be asked at all — a network
// failure, not an answer from the server. It says nothing about the article.
type Unreachable struct {
	EntryID int
	Title   string
	URL     string
	Err     error
}

// Report is the outcome of a scan. WithoutURL and Unreachable are part of the
// answer, not footnotes: together with Undecidable they are everything the scan
// did not settle.
type Report struct {
	Results     []Result
	Unreachable []Unreachable
	// WithoutURL counts entries the scan could not even attempt.
	WithoutURL int
	// TotalEntries is the catalog size, so a reader can see the coverage of
	// this scan without computing it.
	TotalEntries int
}

// Undecidable counts answers that carry no verdict about the article — 403 from
// an anti-bot, a rate limit, a server error. This is the number that decides
// whether a browser is needed.
func (r Report) Undecidable() int {
	n := 0
	for _, res := range r.Results {
		if res.Status.String() == "undecidable" {
			n++
		}
	}
	return n
}

// Actionable returns the results the owner can act on without opening a
// browser — the ones that are genuinely gone.
func (r Report) Actionable() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Status.IsActionable() {
			out = append(out, res)
		}
	}
	return out
}

// Service runs drift scans.
type Service struct {
	loader  CatalogLoader
	checker LinkChecker
}

// NewService returns a Service backed by loader and checker.
func NewService(loader CatalogLoader, checker LinkChecker) *Service {
	return &Service{loader: loader, checker: checker}
}

// Scan asks every entry's URL for its status, stamping each result with now.
func (s *Service) Scan(now time.Time) (Report, error) {
	c, err := s.loader.Load()
	if err != nil {
		return Report{}, err
	}

	rep := Report{TotalEntries: len(c.Entries())}
	for _, e := range c.Entries() {
		url := e.URL()
		if url == "" {
			rep.WithoutURL++
			continue
		}
		code, err := s.checker.Head(url)
		if err != nil {
			rep.Unreachable = append(rep.Unreachable, Unreachable{
				EntryID: e.ID(), Title: e.Title(), URL: url, Err: err,
			})
			continue
		}
		status, err := domain.ClassifyLinkStatus(code)
		if err != nil {
			return Report{}, fmt.Errorf("entry %d: %w", e.ID(), err)
		}
		rep.Results = append(rep.Results, Result{
			EntryID: e.ID(), Title: e.Title(), URL: url,
			Code: code, Status: status, CheckedAt: now,
		})
	}
	return rep, nil
}
