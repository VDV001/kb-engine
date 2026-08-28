package drift_test

import (
	"context"
	"errors"
	"slices"
	"strings"
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

func (s stubChecker) Head(_ context.Context, url string) (drift.Response, error) {
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
	return buildEntry(t, id, url, nil)
}

func buildEntry(t *testing.T, id int, url string, checked *time.Time) domain.Entry {
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
		DriftCheckDate: checked,
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

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(t.Context(), time.Now())
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

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(t.Context(), time.Now())
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
	// Каталог из ТРЁХ записей, а не из одной: на одной записи «каждый» и
	// «первый» — одно и то же, и штамп, проставленный только первому
	// результату, прошёл бы проверку (issue #229). Три разных кода ответа
	// заодно закрывают догадку, что штамп ставится лишь удачным.
	urls := map[string]int{
		"https://example.com/a": 200,
		"https://example.com/b": 301,
		"https://example.com/c": 404,
	}
	c := catalogOf(t,
		entry(t, 1, "https://example.com/a"),
		entry(t, 2, "https://example.com/b"),
		entry(t, 3, "https://example.com/c"),
	)

	rep, err := drift.NewService(fixedLoader{c}, stubChecker{codes: urls}).Scan(t.Context(), when)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rep.Results) != len(urls) {
		t.Fatalf("результатов %d, ждали %d — часть записей не проверена вовсе",
			len(rep.Results), len(urls))
	}
	for _, r := range rep.Results {
		if !r.CheckedAt.Equal(when) {
			t.Errorf("%s: CheckedAt = %v, ждали %v", r.URL, r.CheckedAt, when)
		}
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

	rep, err := svc.Scan(t.Context(), time.Now())
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

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(t.Context(), time.Now())
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

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := rep.Moved(); len(got) != 0 {
		t.Fatalf("Moved() = %+v, want none — there is no new address to write", got)
	}
}

// Ответ, которого нет в HTTP, стоил всего прогона.
//
// Скан — десять минут по живой базе, и он терял всё из-за одного сервера,
// вернувшего код вне 100-599 (так отвечает, например, LinkedIn своим 999).
// Сетевой отказ при этом, наоборот, копился в отчёте и скан продолжался: два
// похожих по природе отказа имели противоположную цену, и ни из имени команды,
// ни из документации это не следовало.
func TestScan_survivesAnAnswerOutsideHTTP(t *testing.T) {
	c := catalogOf(t,
		entry(t, 1, "https://example.com/weird"),
		entry(t, 2, "https://example.com/alive"),
	)
	checker := stubChecker{codes: map[string]int{
		"https://example.com/weird": 999,
		"https://example.com/alive": 200,
	}}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("один непонятный ответ обрушил весь скан: %v", err)
	}
	if got := len(rep.Results); got != 1 {
		t.Errorf("получено %d вердиктов, ожидался 1 (живая ссылка)", got)
	}
	// Непонятый ответ попадает туда же, где живут неответившие: движок не
	// притворяется, что понял его, и не выдумывает вердикт.
	if got := len(rep.Unreachable); got != 1 {
		t.Fatalf("непонятный ответ не назван вовсе: %+v", rep.Unreachable)
	}
	if !strings.Contains(rep.Unreachable[0].Err.Error(), "999") {
		t.Errorf("причина не называет код ответа: %v", rep.Unreachable[0].Err)
	}
}

// Прерванный скан отдаёт то, что успел узнать.
//
// Десять минут работы, и Ctrl-C терял их целиком: каталог трогается один раз в
// самом конце. Отмена — не отказ, а решение человека остановиться, поэтому
// отчёт возвращается частичным и помеченным, а не ошибкой.
func TestScan_returnsWhatItHasWhenStopped(t *testing.T) {
	c := catalogOf(t,
		entry(t, 1, "https://example.com/one"),
		entry(t, 2, "https://example.com/two"),
		entry(t, 3, "https://example.com/three"),
	)
	ctx, cancel := context.WithCancel(t.Context())
	checker := cancelAfterFirst{
		inner:  stubChecker{codes: map[string]int{"https://example.com/one": 200}},
		cancel: cancel,
	}

	rep, err := drift.NewService(fixedLoader{c}, checker).Scan(ctx, time.Now())
	if err != nil {
		t.Fatalf("отмена пришла ошибкой, а не частичным отчётом: %v", err)
	}
	if !rep.Stopped {
		t.Error("отчёт не помечен прерванным — читатель примет его за полный")
	}
	if got := len(rep.Results); got != 1 {
		t.Errorf("получено %d вердиктов, ожидался 1 — успевший ответ должен остаться", got)
	}
	// Неспрошенное названо, а не растворилось: «не дошла очередь» и «проверено»
	// — разные ответы.
	if rep.NotAttempted != 2 {
		t.Errorf("не спрошено %d адресов, ожидалось 2", rep.NotAttempted)
	}
}

// cancelAfterFirst отменяет контекст сразу после первого ответа — так
// воспроизводится Ctrl-C посреди прогона.
type cancelAfterFirst struct {
	inner  stubChecker
	cancel context.CancelFunc
}

func (c cancelAfterFirst) Head(ctx context.Context, url string) (drift.Response, error) {
	resp, err := c.inner.Head(ctx, url)
	c.cancel()
	return resp, err
}

// entryChecked builds an entry that already carries a drift check date.
func entryChecked(t *testing.T, id int, url string, checked time.Time) domain.Entry {
	t.Helper()
	return buildEntry(t, id, url, &checked)
}

// --limit exists so a first run can be a sample. But a sample taken in catalog
// order asks the entries that were checked most recently and never reaches the
// ones that were never checked at all — they sit at the tail, because new
// entries are appended there.
//
// Замер на живой базе 28.08.2026: 1414 записей с url, ровно 6 без даты
// проверки — и это id 1545–1550, последние в каталоге. `--limit 6` спрашивал
// id 1–6, проверенные накануне. Дотянуться до нужных шести можно было только
// полным прогоном в 1414 запросов.
func TestScan_limitPicksTheLeastRecentlyChecked(t *testing.T) {
	old := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		entry []domain.Entry
		limit int
		want  []int // ids expected to be asked, in any order
	}{
		{
			name: "никогда не проверенная идёт первой, хотя лежит в хвосте",
			entry: []domain.Entry{
				entryChecked(t, 1, "https://example.com/a", recent),
				entryChecked(t, 2, "https://example.com/b", recent),
				entry(t, 3, "https://example.com/c"),
			},
			limit: 1,
			want:  []int{3},
		},
		{
			name: "при всех проверенных берётся самая давняя",
			entry: []domain.Entry{
				entryChecked(t, 1, "https://example.com/a", recent),
				entryChecked(t, 2, "https://example.com/b", old),
				entryChecked(t, 3, "https://example.com/c", recent),
			},
			limit: 1,
			want:  []int{2},
		},
		{
			name: "непроверенные исчерпаны — добирается давними",
			entry: []domain.Entry{
				entryChecked(t, 1, "https://example.com/a", recent),
				entryChecked(t, 2, "https://example.com/b", old),
				entry(t, 3, "https://example.com/c"),
			},
			limit: 2,
			want:  []int{2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := catalogOf(t, tt.entry...)
			checker := stubChecker{codes: map[string]int{
				"https://example.com/a": 200,
				"https://example.com/b": 200,
				"https://example.com/c": 200,
			}}
			svc := drift.NewService(fixedLoader{c}, checker)
			svc.Limit = tt.limit

			rep, err := svc.Scan(t.Context(), time.Now())
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got := make([]int, 0, len(rep.Results))
			for _, r := range rep.Results {
				got = append(got, r.EntryID)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("спрошены записи %v, ожидались %v — отбор идёт по порядку каталога, а не по давности проверки", got, want)
			}
			if sum := len(rep.Results) + len(rep.Unreachable) + rep.WithoutURL + rep.NotAttempted; sum != rep.TotalEntries {
				t.Fatalf("buckets sum to %d, catalog has %d", sum, rep.TotalEntries)
			}
		})
	}
}

// Отчёт обязан называть, сколько записей не проверялось НИ РАЗУ. «Проверено 6
// из 1525» звучит как выборка из проверенного массива, а на деле часть базы
// может не спрашиваться никогда: новые записи дописываются в хвост, и до
// починки 28.08.2026 малый --limit до них не доходил вовсе.
//
// Это то же самое требование, что и к остальным полям отчёта: назвать не
// только найденное, но и не установленное.
func TestScan_reportsHowManyWereNeverChecked(t *testing.T) {
	checked := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	c := catalogOf(t,
		entryChecked(t, 1, "https://example.com/a", checked),
		entry(t, 2, "https://example.com/b"),
		entry(t, 3, "https://example.com/c"),
	)
	checker := stubChecker{codes: map[string]int{
		"https://example.com/a": 200,
		"https://example.com/b": 200,
		"https://example.com/c": 200,
	}}

	svc := drift.NewService(fixedLoader{c}, checker)
	svc.Limit = 1

	rep, err := svc.Scan(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Считается состояние ДО прогона: сколько записей входило в него, ни разу
	// не проверенными. Иначе число зависело бы от того, что успел сделать сам
	// прогон, и «сколько ещё не трогали» стало бы невычислимым.
	if rep.NeverChecked != 2 {
		t.Fatalf("NeverChecked = %d, want 2 — отчёт не называет, сколько записей не спрашивали ни разу", rep.NeverChecked)
	}
}

// Отрицательный контроль: у полностью проверенной базы число обязано быть
// нулём, а не «сколько-то». Иначе детектор ругался бы на правду.
func TestScan_neverCheckedIsZeroWhenAllHaveDates(t *testing.T) {
	checked := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	c := catalogOf(t,
		entryChecked(t, 1, "https://example.com/a", checked),
		entryChecked(t, 2, "https://example.com/b", checked),
	)
	checker := stubChecker{codes: map[string]int{
		"https://example.com/a": 200,
		"https://example.com/b": 200,
	}}
	svc := drift.NewService(fixedLoader{c}, checker)

	rep, err := svc.Scan(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if rep.NeverChecked != 0 {
		t.Fatalf("NeverChecked = %d, want 0 — все записи проверены, ругаться не на что", rep.NeverChecked)
	}
}
