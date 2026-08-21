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

	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/finance"

	"github.com/daniil/kb-engine/internal/usecase/freshness"
	"github.com/daniil/kb-engine/internal/usecase/query"
	"github.com/daniil/kb-engine/internal/usecase/search"
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
	// LinkHealth is what the last drift scan learned about the base's own urls.
	// Separate from the findings above because it is not a list of problems but
	// a state of affairs — including how much of the base was never asked.
	LinkHealth() (audit.LinkHealth, error)
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
	// Now returns the active pipeline document together with when it was last
	// edited. Время правки приходит вместе с текстом, а не отдельным вызовом:
	// это одно знание об одном файле, и разъехаться они не должны.
	Now func() (NowDoc, error)
	// Team and Projects return the owner's JSON verbatim — the engine does
	// not reshape content it does not own. Время правки едет рядом, а не
	// отдельным вызовом: это одно знание об одном файле.
	Team     func() (FileDoc, error)
	Projects func() (FileDoc, error)
	// Maps are the architecture maps the engine was pointed at: documents that
	// say how a project actually works, with every claim anchored to a
	// file:line in live code. nil means none were configured — the view then
	// names the flag that would bring them, because an empty page reads as
	// breakage while a named flag reads as a request nobody made yet.
	Maps MapsLoader
	// Media is the owner's image directory, served under /media/. Screenshots
	// referenced from projects.json live there rather than in the bundle, for
	// the same reason the JSON does: they are his content, not the engine's.
	Media fs.FS
	// Artefacts is the knowledge base tree itself, served under /kb/ — but only
	// at the paths the catalog names in an entry's file field. Own artefacts —
	// cheat sheets, courses, write-ups, project pages — used to exist as a row
	// and open through nothing at all: 104 entries carry a file and no url.
	// Deliberately an allow-list rather than a file server: personal notes and
	// finances live in the same tree, and the catalog is what decides which of
	// it the shop window may show.
	Artefacts fs.FS
}

// EngineInfo — сборка, которая прямо сейчас отвечает на запросы. Приходит
// снаружи, а не читается здесь из runtime/debug: адаптер отдаёт то, что ему
// дали, и тогда его можно проверить тестом, не собирая бинарь.
//
// Нужна она не для украшения: подвал предлагает исходники по AGPL §13, а
// предложение без версии не отвечает на вопрос «исходники чего именно».
type EngineInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
	// Sources — какие необязательные файлы движку передали, а какие нет.
	// Приходит снаружи вместе с версией, по той же причине: адаптер не
	// выясняет сам, чем его запустили.
	//
	// Нужно оно затем же, зачем строка о них в логе запуска: вкладка, читающая
	// непереданный файл, иначе может нарисовать только пустоту, а пустота
	// неотличима от «в базе действительно ничего нет».
	Sources []SourceStatus `json:"sources"`
}

// SourceStatus — один необязательный источник и факт его подключения. Флаг
// назван ровно так же, как в командной строке: страница показывает его тому,
// кто будет перезапускать движок, и переименование по дороге превратило бы
// подсказку в совет, который не сработает.
type SourceStatus struct {
	Flag      string `json:"flag"`
	Connected bool   `json:"connected"`
}

// Finances is what the finance port hands over: the ledger rows and the account
// balances, unaggregated. The journal needs the rows themselves — it lists,
// filters and sorts them — so this endpoint stays.
type Finances struct {
	Transactions []domain.Transaction
	Accounts     []domain.Account
	// Confirmations — моменты подтверждения остатков, если они известны. Пустая
	// карта — законное состояние, а не сбой: у счёта, подтверждённого до
	// появления файла состояния, момента нет.
	Confirmations finance.Confirmations
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
// Option — необязательная часть сервера.
//
// Функциональным параметром, а не десятым позиционным: у NewServer их и так
// девять, и каждый новый источник заставлял бы править все вызовы, включая
// десяток тестовых. Отсутствие опции — законное состояние, а не пропуск.
type Option func(*options)

type options struct {
	syn search.Matcher
}

// WithSynonyms подключает слой перевода терминов.
//
// До неё словарь был подключён ТОЛЬКО к терминалу: main.go отдавал его в
// tui.Sources, сервер о нём не знал, и «конкурентность» находила concurrency в
// одной поверхности из двух. Тот же класс, что #252, этажом выше — правило
// одно, а доступ к нему был выдан не всем.
func WithSynonyms(m search.Matcher) Option {
	return func(o *options) { o.syn = m }
}

func NewServer(q Querier, a Auditor, an Analyzer, fin Financier, cfg ConfigLoader, chlog ChangelogLoader, docs Documents, engine EngineInfo, frontend fs.FS, opts ...Option) http.Handler {
	var o options
	for _, apply := range opts {
		// nil — законное «этой части нет»: тот, кто читает необязательный файл,
		// возвращает именно его, когда файла не оказалось. Падать на старте
		// из-за отсутствующего словаря значило бы уронить весь дашборд.
		if apply != nil {
			apply(&o)
		}
	}
	mux := http.NewServeMux()
	m := newMetrics()
	mux.HandleFunc("GET /metrics", handleMetrics(m, q, engine))
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /readyz", handleReadyz(q))
	mux.HandleFunc("GET /api/stats", handleStats(q))
	mux.HandleFunc("GET /api/entries", handleEntries(q))
	mux.HandleFunc("GET /api/search", handleSearch(q, o.syn))
	mux.HandleFunc("GET /api/audits", handleAudits(a))
	mux.HandleFunc("GET /api/duplicates", handleDuplicates(a))
	mux.HandleFunc("GET /api/link-health", handleLinkHealth(a))
	mux.HandleFunc("GET /api/analytics", handleAnalytics(an))
	mux.HandleFunc("GET /api/analytics-config", handleAnalyticsConfig(cfg))
	mux.HandleFunc("GET /api/graph", handleGraph(an, cfg))
	mux.HandleFunc("GET /api/changelog", handleChangelog(chlog))
	mux.HandleFunc("GET /api/engine", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, engine)
	})
	mux.HandleFunc("POST /api/finances/export", handleFinanceExport())
	mux.HandleFunc("GET /api/now", handleNow(docs.Now, q, chlog, fin))
	mux.HandleFunc("GET /api/sources", handleSources(docs, q, chlog, fin, engine))
	mux.HandleFunc("GET /api/team", handleRawJSON(docs.Team))
	mux.HandleFunc("GET /api/projects", handleRawJSON(docs.Projects))
	mux.HandleFunc("GET /api/maps", handleMaps(docs.Maps))
	mux.HandleFunc("GET /api/maps/{id}", handleMap(docs.Maps))
	mux.HandleFunc("GET /api/finances", handleFinances(fin))
	mux.HandleFunc("GET /api/finances/summary", handleFinanceSummary(fin))
	if docs.Media != nil {
		mux.Handle("GET /media/", http.StripPrefix("/media/", mediaHandler(docs.Media)))
	}
	// Регистрируется всегда, а не под флагом: без источника обработчик отвечает
	// 404, и это тот же ответ, что у не названного каталогом пути. Под условием
	// шаблона не было бы вовсе, и запрос артефакта доходил бы до SPA-фолбэка —
	// 200 с разметкой там, где браузер ждёт документ.
	mux.Handle("GET /kb/", kbFilesHandler(docs.Artefacts, q))
	if frontend != nil {
		// Фолбэк на index.html нужен клиентским маршрутам страницы (/archives,
		// /now), но не путям API: опечатка в адресе должна быть 404, а не 200 с
		// разметкой, которую потребитель попытается разобрать как JSON.
		//
		// Порядок разбирает сам ServeMux — он выбирает самый длинный
		// подходящий шаблон. Конкретный "GET /api/stats" побеждает поддерево
		// "/api/", а то, в свою очередь, побеждает "/". Своя проверка префикса
		// внутри spaHandler дала бы то же самое руками.
		mux.Handle("/api/", http.NotFoundHandler())
		// То же самое для медиа. Без --media шаблона "GET /media/" выше нет
		// вовсе, и запрос картинки доходил до фолбэка — 200 с разметкой там,
		// где потребитель ждёт изображение. Когда флаг передан, побеждает
		// конкретный "GET /media/": он матчит подмножество запросов, и
		// ServeMux выбирает более специфичный шаблон.
		mux.Handle("/media/", http.NotFoundHandler())
		mux.Handle("/", spaHandler(frontend))
	}
	// Обёртка снаружи mux, а не middleware на каждом обработчике: ServeMux
	// заполняет Request.Pattern при сопоставлении, поэтому шаблон маршрута
	// известен только после его работы. Регистрация нового эндпоинта попадает
	// в метрики сама, без строчки в отдельном списке.
	return instrument(mux, m)
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
		st, err := q.Stats()
		if err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// Непрочитанные записи — не повод объявлять сервис негодным: витрины
		// работают. Но и молчать нельзя, иначе «ready» будет означать «показано
		// всё», чего никто не проверял.
		if n := len(st.Unreadable); n > 0 {
			fmt.Fprintf(w, "ready, but %d entr(ies) in the catalog could not be read\n", n)
			return
		}
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

// NowDoc — документ «что в работе сейчас» и время его последней правки.
type NowDoc struct {
	Markdown string
	EditedAt time.Time
}

// handleNow отдаёт документ вместе с ответом на вопрос, не отстал ли он.
//
// Страница тухнет тихо: текст остаётся правдоподобным, а мир вокруг уходит
// вперёд. Считать отставание по возрасту файла нельзя — документ, которого не
// касались месяц, но и база вокруг которого не менялась, верен. Поэтому
// сравниваются события: записи, версия базы, операции ПОСЛЕ последней правки.
//
// Проверка необязательна: без каталога, журнала и книги остаётся один markdown,
// и это меньшая страница, а не ошибка.
func handleNow(load func() (NowDoc, error), q Querier, chlog ChangelogLoader, fin Financier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if load == nil {
			writeJSON(w, nil)
			return
		}
		doc, err := load()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"markdown":  doc.Markdown,
			"edited_at": timeOrEmpty(doc.EditedAt),
			"freshness": toFreshnessDTO(checkNowFreshness(doc, q, chlog, fin)),
		})
	}
}

// handleSources отвечает на вопрос «какие страницы отстали» разом.
//
// Отдельно от /api/engine, где перечислено, какие флаги переданы: там про
// запуск, здесь про содержимое. Страница, у которой опор для сверки нет,
// говорит именно это — зелёная галочка означала бы «проверено», а проверки не
// было.
func handleSources(docs Documents, q Querier, chlog ChangelogLoader, fin Financier, engine EngineInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out := []sourceStateDTO{}

		if docs.Now != nil {
			if doc, err := docs.Now(); err == nil {
				r := checkNowFreshness(doc, q, chlog, fin)
				out = append(out, toSourceDTO(freshness.CheckSource(freshness.Source{
					Name: "Now", Flag: "--now", Now: time.Now(), Anchored: true,
					EditedAt: doc.EditedAt, Facts: r.Facts,
				}), r.Draft))
			}
		}
		// Projects знает о самом движке: карточка называет его версию, и она
		// живёт своей жизнью. На живом файле там стояло v0.5.0 при 0.15.0 —
		// страница врала о собственном проекте втрое.
		if docs.Projects != nil {
			if doc, err := docs.Projects(); err == nil {
				var facts []freshness.Fact
				if f := freshness.VersionMention(string(doc.Bytes), "kb-engine", engine.Version); f != nil {
					facts = append(facts, *f)
				}
				// Опора у Projects есть ровно тогда, когда движок знает о себе
				// правду: у сборки из исходников версия псевдо, и сверять с ней
				// нечего.
				out = append(out, toSourceDTO(freshness.CheckSource(freshness.Source{
					Name: "Projects", Flag: "--projects", Now: time.Now(),
					Anchored: freshness.IsReleaseVersion(engine.Version),
					EditedAt: doc.EditedAt, Facts: facts,
				}), ""))
			}
		}
		// У Team опор нет вовсе: состав отдела меняется вне базы, и движку
		// сверять его не с чем. Он показывает дату и возраст и говорит об этом
		// прямо, вместо того чтобы молча выглядеть проверенным.
		if docs.Team != nil {
			if doc, err := docs.Team(); err == nil {
				out = append(out, toSourceDTO(freshness.CheckSource(freshness.Source{
					Name: "Team", Flag: "--team", Now: time.Now(), EditedAt: doc.EditedAt,
				}), ""))
			}
		}
		writeJSON(w, map[string]any{"sources": out})
	}
}

// checkNowFreshness собирает состояние мира для проверки.
//
// Источник, который не отдался, просто не участвует: сказать «отстал», не
// прочитав каталог, — то же самое, что сказать «свеж», не прочитав его.
func checkNowFreshness(doc NowDoc, q Querier, chlog ChangelogLoader, fin Financier) freshness.Report {
	in := freshness.Input{Now: time.Now(), EditedAt: doc.EditedAt}

	if q != nil {
		if entries, err := q.Entries(); err == nil {
			for _, e := range entries {
				if added := e.DateAdded(); added != nil {
					in.Entries = append(in.Entries, freshness.EntryFact{
						ID: e.ID(), Title: e.Title(), Added: *added,
					})
				}
			}
		}
	}
	if chlog != nil {
		if cl, err := chlog(); err == nil {
			// Дата приходит строкой, как её написали в журнале. Нечитаемая — это
			// «не знаю, когда»: тогда версия в расчёт не идёт вовсе, вместо того
			// чтобы попасть туда с нулевым временем и объявить отставание всегда.
			if d, err := time.Parse(time.DateOnly, cl.CurrentDate); err == nil {
				in.Version, in.VersionDate = cl.CurrentVersion, d
			}
		}
	}
	if fin != nil {
		if f, err := fin.Finances(); err == nil {
			for _, t := range f.Transactions {
				in.Operations = append(in.Operations, t.Date())
			}
		}
	}
	return freshness.Check(in)
}

// timeOrEmpty печатает дату, а нулевое время — пустой строкой: «0001-01-01»
// на экране читается как дата, которой не было.
func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// FileDoc — файл владельца и время его последней правки.
type FileDoc struct {
	Bytes    []byte
	EditedAt time.Time
}

// handleRawJSON serves an owner-supplied JSON file byte-for-byte.
func handleRawJSON(load func() (FileDoc, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if load == nil {
			writeJSON(w, nil)
			return
		}
		doc, err := load()
		if err != nil {
			writeError(w, err)
			return
		}
		if !json.Valid(doc.Bytes) {
			// Отдать битый файл — значит сломать view молча; ошибка честнее.
			writeError(w, fmt.Errorf("document is not valid JSON"))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(doc.Bytes)
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

// handleGraph serves the computed topology with the owner's labels laid over
// it. The two come from different places on purpose: the catalog knows which
// categories are connected, and only the config knows what he calls the link.
func handleGraph(an Analyzer, cfg ConfigLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		g, err := an.Graph()
		if err != nil {
			writeError(w, err)
			return
		}
		// A missing or unreadable config must not cost the topology: the graph
		// is still true without labels, it is only less explained.
		if c, err := cfg(); err == nil {
			g = analytics.LabelEdges(g, c.Graph)
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

// handleSearch отвечает тем же usecase, которым ищет терминал.
//
// Отдельный эндпоинт нужен именно ради этого: пока поиска в API не было, фронт
// забирал весь каталог и фильтровал у себя подстрокой — вторая реализация
// одного правила, разошедшаяся с первой на измеримую величину (#252).
//
// Пустой q возвращает весь каталог, а не ошибку: так ведёт себя терминал, и
// расхождение здесь было бы тем же дефектом в миниатюре.
func handleSearch(q Querier, syn search.Matcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := q.Entries()
		if err != nil {
			writeError(w, err)
			return
		}
		// FilterWith, а не Filter: слой перевода приходит снаружи, и его
		// нулевое значение — законное «словаря нет», а не поломка.
		found := search.FilterWith(entries, r.URL.Query().Get("q"), syn)
		dtos := make([]entryDTO, 0, len(found))
		for _, e := range found {
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

// handleLinkHealth serves the drift summary. Its own endpoint rather than a
// field on /api/audits: that one answers with lists of findings, and a счётчик
// among them would have to pretend to be a finding to fit the shape.
func handleLinkHealth(a Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h, err := a.LinkHealth()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, h)
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
			// Остаток считает тот же usecase, что и терминал: две реализации
			// одной арифметики однажды разойдутся, и разойдутся молча.
			for _, b := range finance.CurrentBalances(f.Accounts, f.Transactions, f.Confirmations) {
				accounts = append(accounts, toBalanceDTO(b))
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
		// «Дублей нет» — это пустой список, а не отсутствие ответа. Go пишет
		// nil-слайс как null, и пока в каталоге находилась хоть одна группа,
		// разницы никто не замечал. Когда починка дедупа убрала последнюю,
		// клиент взял длину у null и снял всё дерево — пустая страница вместо
		// дашборда. Соседние эндпоинты (entries, finances) уже отвечают пустым
		// списком; этот выбивался из общего правила.
		if groups == nil {
			groups = []audit.DuplicateGroup{}
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
