package httpapi_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

type fakeQuery struct{}

func (fakeQuery) Stats() (query.Stats, error) {
	return query.Stats{
		Total:          2,
		ByCategory:     map[string]int{"golang": 2},
		CategoryLabels: map[string]string{"golang": "Go: язык и экосистема"},
	}, nil
}

func (fakeQuery) Health() (query.Health, error) {
	return query.Health{Total: 4, Processed: 3, WithNotes: 1, NotesBase: 3}, nil
}

func (fakeQuery) Entries() ([]domain.Entry, error) {
	habrID := 1
	rs, _ := domain.NewReadState("read")
	cat, _ := domain.NewCategory("golang")
	lc, _ := domain.NewLifecycle("active")
	v, _ := domain.NewVerdict("keep")
	added := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	habrPublished := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	deepRead := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	e, _ := domain.NewEntry(domain.EntryParams{
		ID: 1, Kind: "article", Title: "Hello", Category: cat, Lifecycle: lc,
		HabrID: &habrID, URL: "https://h/x", ReadState: &rs, Verdict: &v,
		Tags: []string{"go"}, DateAdded: &added, DateCreated: &created,
		RelatedIDs: []int{2},
		// Дата выхода у автора и дата глубокого разбора: обе живут в каталоге и
		// до сих пор не доезжали до вида.
		HabrDate: &habrPublished, DeepReadDate: &deepRead,
	})
	// Разбор — отдельная запись, а не поле первой: после ADR-0004 конспект
	// живёт собственной записью, у которой есть файл и нет адреса.
	wcat, _ := domain.NewCategory("writeups")
	w, _ := domain.NewEntry(domain.EntryParams{
		ID: 2, Kind: "article", Title: "Разбор: Hello", Category: wcat, Lifecycle: lc,
		ReadState: &rs, NotesFile: "notes/2026-08-02_hello.md", DateAdded: &added,
	})
	return []domain.Entry{e, w}, nil
}

type fakeAudit struct{}

func (fakeAudit) OutdatedCandidates() ([]audit.Finding, error) {
	return []audit.Finding{{EntryID: 1, Title: "Hello", Current: "active", Reasons: []string{"keyword:removed"}}}, nil
}
func (fakeAudit) CanonicalCandidates() ([]audit.Finding, error) { return nil, nil }
func (fakeAudit) SupersessionIssues() ([]audit.Finding, error)  { return nil, nil }
func (fakeAudit) LinkHealth() (audit.LinkHealth, error) {
	return audit.LinkHealth{Alive: 4, Moved: 2, Gone: 1, Undecidable: 2, Unchecked: 1, WithURL: 10}, nil
}
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

// Summary — часть порта; тестам этого файла достаточно пустой сводки, разрезы
// проверяются в finance_summary_test.go.
func (f fakeFinance) Summary([]string) (finance.Summary, error) {
	if f.err != nil {
		return finance.Summary{}, f.err
	}
	return finance.Summary{}, nil
}

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

var testEngine = httpapi.EngineInfo{
	Version: "0.9.9",
	Commit:  "abc1234",
	Built:   "2026-07-31T17:21:48Z",
}

func newTestServer() http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) {
			return changelog.Document{CurrentVersion: "0.9.0"}, nil
		}, httpapi.Documents{}, testEngine, nil)
}

// Версия движка живёт в бинаре, и на странице её иначе не показать. Нужна она
// не из любопытства: подвал предлагает исходники по AGPL §13, а предложение без
// версии неполно — непонятно, исходники какой именно сборки предлагаются.
func TestServer_engine(t *testing.T) {
	rec := get(t, newTestServer(), "/api/engine")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Built   string `json:"built"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for field, got := range map[string]string{
		"version": body.Version, "commit": body.Commit, "built": body.Built,
	} {
		want := map[string]string{
			"version": "0.9.9", "commit": "abc1234", "built": "2026-07-31T17:21:48Z",
		}[field]
		if got != want {
			t.Errorf("engine[%q] = %q, want %q", field, got, want)
		}
	}
}

// Тот же вопрос, что задают логу запуска, страница должна уметь задать по HTTP:
// какие источники движку передали, а какие нет. Без этого вкладка, читающая
// непереданный файл, может только нарисовать пустоту — и пустота неотличима от
// «в базе действительно ничего нет».
//
// Список приходит снаружи вместе с версией: адаптер отдаёт то, что ему дали, и
// не выясняет сам, чем его запустили.
func TestServer_engine_sources(t *testing.T) {
	engine := httpapi.EngineInfo{
		Version: "0.9.9",
		Sources: []httpapi.SourceStatus{
			{Flag: "analytics-config", Connected: false},
			{Flag: "ledger", Connected: true},
		},
	}
	h := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, engine, nil)

	rec := get(t, h, "/api/engine")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Sources []struct {
			Flag      string `json:"flag"`
			Connected bool   `json:"connected"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sources) != 2 {
		t.Fatalf("sources = %d, want 2: %s", len(body.Sources), rec.Body.String())
	}
	if body.Sources[0].Flag != "analytics-config" || body.Sources[0].Connected {
		t.Errorf("первый источник = %+v, ждали неподключённый analytics-config", body.Sources[0])
	}
	if body.Sources[1].Flag != "ledger" || !body.Sources[1].Connected {
		t.Errorf("второй источник = %+v, ждали подключённый ledger", body.Sources[1])
	}
}

// The journal needs the rows themselves — it lists, filters and sorts them — so
// this endpoint serves them unaggregated. Totals now come from
// /api/finances/summary rather than being re-derived on the client.
//
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
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, httpapi.Documents{}, testEngine, nil)
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
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, httpapi.Documents{}, testEngine, nil)
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
	if len(entries) != 2 || entries[0]["id"].(float64) != 1 || entries[0]["title"] != "Hello" {
		t.Errorf("entries = %v", entries)
	}
	// The catalog view sorts and displays by the date an entry joined the
	// catalog; without it every row reads "—" and sorting is by id pretending
	// to be chronology.
	if entries[0]["date_added"] != "2026-07-11" {
		t.Errorf("date_added = %v, want 2026-07-11", entries[0]["date_added"])
	}
	// Оба поля даты должны переезжать через границу API. Домен и адаптер
	// каталога читают их оба, но DTO отдавал только date_added — и 461 запись
	// из 1340 приезжала на фронт без даты вовсе, хотя дата у них есть.
	if entries[0]["date_created"] != "2026-04-15" {
		t.Errorf("date_created = %v, want 2026-04-15", entries[0]["date_created"])
	}
}

// Связь «статья → её разбор» и путь к собственному тексту записи обязаны
// доехать до фронта: после ADR-0004 разбор — отдельная запись, и без этих
// двух полей 357 связей и 122 файла живой базы для дашборда не существуют.
func TestServer_entriesCarryWriteupLink(t *testing.T) {
	rec := get(t, newTestServer(), "/api/entries")
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	related, ok := entries[0]["related_ids"].([]any)
	if !ok || len(related) != 1 || related[0].(float64) != 2 {
		t.Errorf("related_ids = %v, want [2]", entries[0]["related_ids"])
	}
	// У статьи файла нет — поле не должно появляться пустым: «нет разбора» и
	// «разбор по пустому пути» на экране выглядели бы одинаково.
	if _, present := entries[0]["file"]; present {
		t.Errorf("file = %v, want absent for an entry without one", entries[0]["file"])
	}

	if entries[1]["file"] != "notes/2026-08-02_hello.md" {
		t.Errorf("file = %v, want the write-up path", entries[1]["file"])
	}
	if _, present := entries[1]["related_ids"]; present {
		t.Errorf("related_ids = %v, want absent: связь односторонняя", entries[1]["related_ids"])
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

// emptyAudit — каталог, в котором аудиту нечего сказать. Ровно это и случилось
// на живых данных: после починки дедупа последняя ложная группа исчезла.
type emptyAudit struct{}

func (emptyAudit) OutdatedCandidates() ([]audit.Finding, error)  { return nil, nil }
func (emptyAudit) CanonicalCandidates() ([]audit.Finding, error) { return nil, nil }
func (emptyAudit) SupersessionIssues() ([]audit.Finding, error)  { return nil, nil }
func (emptyAudit) Duplicates() ([]audit.DuplicateGroup, error)   { return nil, nil }
func (emptyAudit) LinkHealth() (audit.LinkHealth, error)         { return audit.LinkHealth{}, nil }

// Пустой список — это `[]`, а не `null`. Разница не косметическая: nil-слайс
// уходит в JSON как null, клиент считает его длину, и вся страница снимается
// с одной непроверенной точки. Клиент от этого защищён отдельно, но контракт
// чинится здесь: «находок нет» — законный ответ, и выглядеть он должен как
// пустой список, а не как отсутствие ответа.
func TestServer_duplicates_emptyIsAnArrayNotNull(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, emptyAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)

	rec := get(t, srv, "/api/duplicates")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

func TestServer_unknownRoute(t *testing.T) {
	rec := get(t, newTestServer(), "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Тот же вопрос, но серверу, который действительно отгружается.
//
// TestServer_unknownRoute выше строит сервер БЕЗ фронтенда (последний аргумент
// nil) — то есть без index.html-фолбэка, и потому получает честный 404. В
// выпущенном бинаре фронтенд вшит, фолбэк ловит любой путь, и `/api/nope`
// отвечает 200 с HTML. Зелёный тест утверждал поведение, которого у сборки нет.
//
// Поймано не чтением кода: при проверке выпуска 0.17.0 прогон эндпоинтов дал
// 200 на `/api/finance` — маршрута с таким именем не существует, настоящий
// `/api/finances`. Код ответа не различал их, потому что различать было нечем.
//
// Цена не в дашборде (он ходит по верным путям), а в потребителе API: опечатка
// в пути даёт 200 и HTML вместо 404, то есть провал, который на его стороне
// выглядит успехом.
func TestServer_unknownAPIRouteWithFrontend(t *testing.T) {
	frontend := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>")}}
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) {
			return changelog.Document{CurrentVersion: "0.9.0"}, nil
		}, httpapi.Documents{}, testEngine, frontend)

	t.Run("несуществующий путь под /api/ — 404, а не страница", func(t *testing.T) {
		rec := get(t, srv, "/api/nope")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (тело: %.40s)", rec.Code, rec.Body.String())
		}
	})

	// Контроль в обе стороны: починка не должна ни сломать настоящий маршрут,
	// ни отобрать у SPA её собственный фолбэк для клиентских путей.
	t.Run("настоящий маршрут по-прежнему отвечает", func(t *testing.T) {
		if rec := get(t, srv, "/api/stats"); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("клиентский путь вне /api/ по-прежнему отдаёт страницу", func(t *testing.T) {
		rec := get(t, srv, "/archives")
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "<html>" {
			t.Errorf("body = %q, want index.html", got)
		}
	})
}

// Та же грабля, что закрыта для /api/ в #149, но на втором поддереве.
//
// Маршрут /media/ регистрируется условно — только когда передан --media. Без
// флага шаблона в mux нет вовсе, запрос доходит до фолбэка и получает 200 с
// разметкой index.html. Потребитель картинки читает это как успех и пытается
// разобрать HTML как изображение.
//
// Контроль в обе стороны обязателен: подсадка «отдавать 404 всегда» должна
// валить подтест с флагом, иначе проверка утверждала бы, что /media/ сломан
// целиком, и была бы зелёной на сломанной сборке.
func TestServer_mediaRouteWithFrontend(t *testing.T) {
	frontend := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>")}}
	newSrv := func(media fs.FS) http.Handler {
		return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
			func() (analyticsconfig.Config, error) { return testConfig, nil },
			func() (changelog.Document, error) {
				return changelog.Document{CurrentVersion: "0.9.0"}, nil
			}, httpapi.Documents{Media: media}, testEngine, frontend)
	}

	t.Run("без --media путь под /media/ — 404, а не страница", func(t *testing.T) {
		rec := get(t, newSrv(nil), "/media/nope.png")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (тело: %.40s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("с --media настоящий файл по-прежнему отдаётся", func(t *testing.T) {
		media := fstest.MapFS{"floq.png": &fstest.MapFile{Data: []byte("PNG")}}
		rec := get(t, newSrv(media), "/media/floq.png")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "PNG" {
			t.Errorf("body = %q, want the file", got)
		}
	})

	t.Run("с --media отсутствующий файл — 404, а не страница", func(t *testing.T) {
		media := fstest.MapFS{"floq.png": &fstest.MapFile{Data: []byte("PNG")}}
		rec := get(t, newSrv(media), "/media/nope.png")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (тело: %.40s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("клиентский путь вне /media/ по-прежнему отдаёт страницу", func(t *testing.T) {
		rec := get(t, newSrv(nil), "/projects")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "<html>" {
			t.Errorf("body = %q, want index.html", got)
		}
	})
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
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil, httpapi.Documents{}, testEngine, nil)
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

// Now, Team and Projects are personal content served from files the owner
// points the engine at — the repo carries only the renderer. All three read
// per request, so editing the file updates the view on the next reload, and
// all three are optional: unconfigured is a valid deployment, not an error.
func TestServer_documents(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil,
		httpapi.Documents{
			Now: func() (httpapi.NowDoc, error) {
				return httpapi.NowDoc{Markdown: "# Сейчас\n\n- работа"}, nil
			},
			Team: func() (httpapi.FileDoc, error) {
				return httpapi.FileDoc{Bytes: []byte(`{"title":"Team"}`)}, nil
			},
			Projects: nil, // не настроен
		}, testEngine, nil)

	rec := get(t, srv, "/api/now")
	if rec.Code != http.StatusOK {
		t.Fatalf("now status = %d", rec.Code)
	}
	var now map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &now); err != nil {
		t.Fatalf("decode now: %v", err)
	}
	if !strings.Contains(now["markdown"].(string), "# Сейчас") {
		t.Errorf("now markdown = %q", now["markdown"])
	}

	rec = get(t, srv, "/api/team")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"Team"`) {
		t.Errorf("team = %d %q", rec.Code, rec.Body.String())
	}
	// JSON-файл владельца отдаётся как есть — движок не пересобирает чужой
	// контент, поэтому Content-Type обязан остаться JSON.
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("team content-type = %q", ct)
	}

	rec = get(t, srv, "/api/projects")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Errorf("unconfigured projects = %d %q, want 200 null", rec.Code, rec.Body.String())
	}
}

// Сайдбар архива печатает названия категорий, а не их ключи, поэтому словарь
// обязан доехать до фронта — считать его там неоткуда.
func TestServer_statsCarriesCategoryLabels(t *testing.T) {
	rec := get(t, newTestServer(), "/api/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	labels, ok := st["category_labels"].(map[string]any)
	if !ok {
		t.Fatalf("category_labels = %v, want an object", st["category_labels"])
	}
	if labels["golang"] != "Go: язык и экосистема" {
		t.Errorf("label of golang = %v, want %q", labels["golang"], "Go: язык и экосистема")
	}
}

// Страница «что в работе сейчас» должна говорить, что отстала, — иначе она
// тухнет тихо: текст остаётся правдоподобным, а база уходит вперёд.
func TestServer_now_reportsThatThePageFellBehind(t *testing.T) {
	// Раньше и записи каталога фикстуры (11.07), и её траты (01.07): обе
	// случились после правки, значит страница отстала от обеих.
	edited := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil,
		httpapi.Documents{
			Now: func() (httpapi.NowDoc, error) {
				return httpapi.NowDoc{Markdown: "# Сейчас", EditedAt: edited}, nil
			},
		}, testEngine, nil)

	rec := get(t, srv, "/api/now")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Markdown  string `json:"markdown"`
		EditedAt  string `json:"edited_at"`
		Freshness struct {
			Behind  bool `json:"behind"`
			Unknown bool `json:"unknown"`
			Facts   []struct {
				Kind string `json:"kind"`
			} `json:"facts"`
			Draft string `json:"draft"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Markdown == "" || body.EditedAt == "" {
		t.Fatalf("документ или дата правки не отданы: %+v", body)
	}
	if !body.Freshness.Behind {
		t.Errorf("отставание не объявлено: %+v", body.Freshness)
	}
	if body.Freshness.Draft == "" {
		t.Error("черновик блока не предложен")
	}
	if len(body.Freshness.Facts) == 0 {
		t.Error("причины отставания не названы")
	}
}

// Пустой список фактов уезжает как [], а не null: nil-слайс Go пишется как
// null, и `.length` у него роняет всё дерево React — этой граблёй дашборд уже
// белел целиком.
func TestServer_now_freshnessFactsAreNeverNull(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil,
		httpapi.Documents{
			Now: func() (httpapi.NowDoc, error) { return httpapi.NowDoc{Markdown: "# Сейчас"}, nil },
		}, testEngine, nil)

	if body := get(t, srv, "/api/now").Body.String(); strings.Contains(body, `"facts":null`) {
		t.Errorf("факты уехали как null:\n%s", body)
	}
}

// Страницы Team и Projects тухнут так же, как Now, но смотрят на них реже —
// значит врут дольше. Один эндпоинт отвечает про все сразу, и у каждой честно
// назван её случай: отстала, свежая или сверять не с чем.
func TestServer_sources(t *testing.T) {
	edited := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil,
		httpapi.Documents{
			Now: func() (httpapi.NowDoc, error) {
				return httpapi.NowDoc{Markdown: "# Сейчас", EditedAt: edited}, nil
			},
			// Карточка называет версию движка, и она отстала на несколько
			// выпусков — ровно то, что нашлось на живом файле владельца.
			Projects: func() (httpapi.FileDoc, error) {
				return httpapi.FileDoc{
					Bytes:    []byte(`{"note":"v0.5.0 · github.com/VDV001/kb-engine"}`),
					EditedAt: edited,
				}, nil
			},
			Team: func() (httpapi.FileDoc, error) {
				return httpapi.FileDoc{Bytes: []byte(`{"title":"Team"}`), EditedAt: edited}, nil
			},
		}, testEngine, nil)

	rec := get(t, srv, "/api/sources")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Sources []struct {
			Name      string `json:"name"`
			Behind    bool   `json:"behind"`
			NoAnchors bool   `json:"no_anchors"`
			AgeDays   int    `json:"age_days"`
			Facts     []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"facts"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sources) != 3 {
		t.Fatalf("источников = %d, ожидалось 3: %+v", len(body.Sources), body.Sources)
	}

	by := map[string]int{}
	for i, s := range body.Sources {
		by[s.Name] = i
	}
	if p := body.Sources[by["Projects"]]; !p.Behind || len(p.Facts) == 0 ||
		!strings.Contains(p.Facts[0].Text, "v0.5.0") {
		t.Errorf("Projects не назвал отставшую версию: %+v", p)
	}
	// Team сверять не с чем — и это отдельное состояние, не «всё хорошо».
	if tm := body.Sources[by["Team"]]; tm.Behind || !tm.NoAnchors {
		t.Errorf("Team: behind=%v no_anchors=%v, ожидалось false/true", tm.Behind, tm.NoAnchors)
	}
	// Возраст называется даже там, где он не приговор.
	if tm := body.Sources[by["Team"]]; tm.AgeDays <= 0 {
		t.Errorf("возраст не посчитан: %d", tm.AgeDays)
	}
}
