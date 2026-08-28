// Package drift checks whether the URLs in the catalog still resolve.
//
// Its contract is unusual and deliberate: the report carries what the scan
// could NOT establish as prominently as what it could. A knowledge base fills
// up with stale entries not because checks fail, but because they quietly
// cover less than the reader assumes.
package drift

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// CatalogLoader is the port for obtaining the catalog (DIP: declared with its
// consumer).
type CatalogLoader interface {
	Load() (*domain.Catalog, error)
}

// Response is what one URL answered: the status code and, for a redirect, the
// address it points at.
type Response struct {
	Code     int
	Location string
}

// LinkChecker asks one URL for its status. Implementations do network I/O.
//
// Контекст здесь потому, что скан живой базы идёт минутами: без него человек не
// может остановить его иначе, чем потеряв весь прогон.
type LinkChecker interface {
	Head(ctx context.Context, url string) (Response, error)
}

// Result is what the scan learned about one entry's URL.
type Result struct {
	EntryID int
	Title   string
	URL     string
	Code    int
	Status  domain.LinkStatus
	// Location is where a redirect points. Empty for everything else.
	Location  string
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
	// NotAttempted counts entries left untouched because Limit was reached. A
	// partial scan must never read as a complete one.
	NotAttempted int
	// TotalEntries is the catalog size, so a reader can see the coverage of
	// this scan without computing it.
	TotalEntries int
	// NeverChecked counts entries whose url was never asked at all — считается
	// по состоянию ДО прогона. «Проверено 6 из 1525» читается как выборка из
	// проверенного массива, хотя часть базы может не спрашиваться никогда:
	// новые записи дописываются в хвост. Число обязано считаться до прогона,
	// иначе оно зависело бы от того, что успел сделать сам прогон, и вопрос
	// «сколько ещё не трогали» стал бы невычислимым.
	NeverChecked int
	// Stopped — скан прерван человеком и оборван на середине. Отдельно от
	// NotAttempted: «не дошла очередь из-за --limit» это выбранная выборка, а
	// прерванный прогон — незаконченная работа, и читать их одинаково нельзя.
	Stopped bool
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

// Moved returns entries whose address redirects somewhere else, together with
// the target. A redirect that names no target is left out: there is nothing to
// act on.
func (r Report) Moved() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Status.String() == "moved" && res.Location != "" {
			out = append(out, res)
		}
	}
	return out
}

// Service runs drift scans.
type Service struct {
	loader  CatalogLoader
	checker LinkChecker
	// Limit caps how many urls are asked (0 = all). A full pass over the live
	// catalog is 1313 requests, so a first run is usually a sample.
	Limit int
}

// NewService returns a Service backed by loader and checker.
func NewService(loader CatalogLoader, checker LinkChecker) *Service {
	return &Service{loader: loader, checker: checker}
}

// scanOrder decides which entries a capped run asks first: never-checked
// entries, then the least recently checked.
//
// Порядок каталога для выборки не годится: новые записи дописываются в хвост,
// поэтому именно они дольше всех остаются непроверенными, а `--limit`
// раз за разом спрашивал начало списка. Замер 28.08.2026 на живой базе — 1414
// записей с url, шесть без даты проверки, и все шесть последние по порядку:
// `--limit 6` не доставал до них вовсе, помогал только полный прогон.
//
// Без предела порядок не трогаем: полный прогон всё равно обойдёт всех, а
// стабильный порядок каталога делает вывод сопоставимым между запусками.
func scanOrder(entries []domain.Entry, limit int) []domain.Entry {
	if limit <= 0 {
		return entries
	}
	out := slices.Clone(entries)
	slices.SortStableFunc(out, func(a, b domain.Entry) int {
		da, db := a.DriftCheckDate(), b.DriftCheckDate()
		switch {
		case da == nil && db == nil:
			return 0
		case da == nil:
			return -1 // никогда не проверенная идёт раньше любой проверенной
		case db == nil:
			return 1
		default:
			return da.Compare(*db) // дальше — от самой давней к свежей
		}
	})
	return out
}

// Scan asks every entry's URL for its status, stamping each result with now.
//
// Отмена контекста прекращает опрос и возвращает то, что уже получено, с
// пометкой Stopped: прогон по живой базе занимает минуты, и терять его целиком
// из-за решения остановиться — цена, которой ничто не оправдывает.
func (s *Service) Scan(ctx context.Context, now time.Time) (Report, error) {
	c, err := s.loader.Load()
	if err != nil {
		return Report{}, err
	}

	rep := Report{TotalEntries: len(c.Entries())}
	for _, e := range c.Entries() {
		if e.URL() != "" && e.DriftCheckDate() == nil {
			rep.NeverChecked++
		}
	}
	asked := 0
	for _, e := range scanOrder(c.Entries(), s.Limit) {
		url := e.URL()
		if url == "" {
			rep.WithoutURL++
			continue
		}
		if s.Limit > 0 && asked >= s.Limit {
			rep.NotAttempted++
			continue
		}
		if ctx.Err() != nil {
			rep.Stopped = true
			rep.NotAttempted++
			continue
		}
		asked++
		resp, err := s.checker.Head(ctx, url)
		if err != nil {
			rep.Unreachable = append(rep.Unreachable, Unreachable{
				EntryID: e.ID(), Title: e.Title(), URL: url, Err: err,
			})
			continue
		}
		status, err := domain.ClassifyLinkStatus(resp.Code)
		if err != nil {
			// Ответ, которого нет в HTTP (999 у LinkedIn и подобное), — это не
			// повод выбросить весь прогон. Он идёт туда же, где живут
			// неответившие: движок не понял ответа и говорит об этом, вместо
			// того чтобы выдумать вердикт или обрушить скан.
			rep.Unreachable = append(rep.Unreachable, Unreachable{
				EntryID: e.ID(), Title: e.Title(), URL: url,
				Err: fmt.Errorf("ответ вне HTTP: %w", err),
			})
			continue
		}
		rep.Results = append(rep.Results, Result{
			EntryID: e.ID(), Title: e.Title(), URL: url,
			Code: resp.Code, Status: status, Location: resp.Location, CheckedAt: now,
		})
	}
	return rep, nil
}
