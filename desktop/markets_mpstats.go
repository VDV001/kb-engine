package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Прогноз» берет нишу из MPStats (отзыв владельца 13.11): обе ниши — одним
// источником «mpstats:<id подключения>|<путь категории Ozon>». Сейчас — те же
// 90 дней до вчера, что свои продажи из кабинета; прошлый год — эти дни годом
// раньше и mpstatsHorizon дней после них: они и прогнозируются.
const mpstatsHorizon = 120

// mpstatsBase — адрес API: в окне настоящий, в тестах — поддельный.
func (f *folderSettings) mpstatsBase() string {
	if f.mpstatsURL != "" {
		return f.mpstatsURL
	}
	return mpstats.DefaultURL
}

// mpstatsTTL — сколько ряд ниши живет в памяти окна: «Посчитать», «Посчитать
// моделью» и пересчет со своим файлом не тратят тариф заново (ревью #605).
// ponytail: память процесса; потолок — после перезапуска окна ряд качается
// снова; путь — кэш на диске, если тариф станет тесным.
const mpstatsTTL = 10 * time.Minute

type nicheEntry struct {
	at     time.Time
	series [2]domain.DailySales
	files  []forecastFile
}

// topCache keeps the top categories of one period for mpstatsTTL: 34
// категории — около 70 запросов и 3-4 минуты (замер 14.11).
type topCache struct {
	mu  sync.Mutex
	key string
	at  time.Time
	v   nicheTable
}

func (c *topCache) get(k string, now time.Time) (nicheTable, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v, c.key == k && now.Sub(c.at) < mpstatsTTL
}

func (c *topCache) put(k string, v nicheTable, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key, c.at, c.v = k, now, v
}

// tableCache keeps niche tables without season by «id|путь|дни» for mpstatsTTL.
type tableCache struct {
	mu sync.Mutex
	m  map[string]tableEntry
}

type tableEntry struct {
	at time.Time
	b  nicheBase
}

func (c *tableCache) get(k string, now time.Time) (nicheBase, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	return e.b, ok && now.Sub(e.at) < mpstatsTTL
}

func (c *tableCache) put(k string, b nicheBase, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]tableEntry{}
	}
	c.m[k] = tableEntry{at: now, b: b}
}

// nicheCache keeps MPStats niche series by «id|путь|сегодня».
type nicheCache struct {
	mu sync.Mutex
	m  map[string]nicheEntry
}

func (c *nicheCache) get(k string, now time.Time) (nicheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	return e, ok && now.Sub(e.at) < mpstatsTTL
}

func (c *nicheCache) put(k string, e nicheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]nicheEntry{}
	}
	c.m[k] = e
}

// nicheSeries reads the two niche series: из MPStats или файлами.
func (f *folderSettings) nicheSeries(ctx context.Context, hc *http.Client, last, now string, today time.Time) ([2]domain.DailySales, []forecastFile, error) {
	var series [2]domain.DailySales
	lastMP, nowMP := strings.HasPrefix(last, "mpstats:"), strings.HasPrefix(now, "mpstats:")
	switch {
	case !lastMP && !nowMP:
		return readNiche(last, now)
	case last != now: // обе — из MPStats одним источником или обе файлами (ревью #605)
		return series, nil, errors.New("обе ниши — из MPStats или обе файлами: нажмите «Из MPStats» еще раз или выберите оба файла")
	}
	src := strings.TrimPrefix(last, "mpstats:")
	cl, path, err := f.mpstatsSource(src, hc) // до кэша: выключенное или удаленное подключение не отдает и запомненное (ревью 14.11)
	if err != nil {
		return series, nil, err
	}
	key := src + "|" + today.Format(time.DateOnly)
	if e, ok := f.mpstatsNiche.get(key, today); ok {
		return e.series, e.files, nil
	}
	from, to := ozonWindow(today)
	spans := [2][2]time.Time{{from.AddDate(-1, 0, 0), to.AddDate(-1, 0, mpstatsHorizon)}, {from, to}}
	var files [2]forecastFile
	// Ряды — по очереди: оба сразу — до 8 запросов, и MPStats рвал соединение
	// (живая проверка 13.11); внутри ряда части и так идут по 4.
	for i, sp := range spans {
		var err error
		if series[i], files[i], err = mpstatsSpan(ctx, cl, path, sp[0], sp[1]); err != nil {
			return series, nil, err
		}
	}
	f.mpstatsNiche.put(key, nicheEntry{at: today, series: series, files: files[:]})
	return series, files[:], nil
}

// mpstatsSpan reads one niche series for days from..to.
func mpstatsSpan(ctx context.Context, cl mpstats.Client, path string, from, to time.Time) (domain.DailySales, forecastFile, error) {
	days, err := cl.CategoryByDay(ctx, path, from, to)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, errors.New(mpstatsMessage(err))
	}
	ds := make([]domain.DaySales, 0, len(days))
	for _, d := range days {
		ds = append(ds, domain.DaySales{Day: d.Day, Value: d.Sales})
	}
	s, err := domain.NewDailySales(ds)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, err
	}
	got := s.Days()
	if len(got) == 0 {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("в MPStats нет продаж ниши «%s» за %s – %s: такого пути у MPStats нет — имена ниш из выгрузки файлом отличаются от категорий MPStats (например, «Пижамы женские», а не «Пижамы»). Надежнее всего: «Ниша → Из MPStats», найти нишу и нажать «Прогноз по этой нише»", path, from.Format("02.01.2006"), to.Format("02.01.2006"))
	}
	return s, forecastFile{File: "MPStats · " + path, Format: "mpstats", Days: len(got),
		From: got[0].Day.Format("02.01.2006"), To: got[len(got)-1].Day.Format("02.01.2006")}, nil
}

// mpstatsPath cleans a category path: пробелы у частей и пустые части прочь —
// путь копируют с вида «Одежда / Куртки» (живая проверка 13.11).
func mpstatsPath(p string) string {
	var parts []string
	for x := range strings.SplitSeq(p, "/") {
		if x = strings.TrimSpace(x); x != "" {
			parts = append(parts, x)
		}
	}
	return strings.Join(parts, "/")
}

// mpstatsSource resolves «<id>|<путь>» into a client with the saved key.
func (f *folderSettings) mpstatsSource(src string, hc *http.Client) (mpstats.Client, string, error) {
	id, path, _ := strings.Cut(src, "|")
	path = mpstatsPath(path)
	if strings.TrimSpace(path) == "" {
		return mpstats.Client{}, "", errors.New("не выбрана ниша: путь категории Ozon, как в MPStats")
	}
	if _, ok := f.snapshot().cfg.Connection(id); !ok { // запомненное подключение удалили — берем нынешнее
		cl, err := f.mpstatsConnClient(hc)
		return cl, path, err
	}
	cl, err := f.mpstatsByID(id, hc)
	return cl, path, err
}

// mpstatsByID returns a client of MPStats connection id with its key.
func (f *folderSettings) mpstatsByID(id string, hc *http.Client) (mpstats.Client, error) {
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnMPStats {
		return mpstats.Client{}, errors.New("подключения MPStats нет — добавьте ключ в Настройках → Подключения")
	}
	if !c.Enabled {
		return mpstats.Client{}, fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(connKeyAccount(id))
	if err != nil {
		return mpstats.Client{}, errors.New("ключа MPStats нет в Связке ключей — добавьте его заново")
	}
	return mpstats.Client{BaseURL: f.mpstatsBase(), Token: key, HTTP: hc}, nil
}

// mpstatsConnClient returns a client of the first enabled MPStats connection.
func (f *folderSettings) mpstatsConnClient(hc *http.Client) (mpstats.Client, error) {
	cl, _, err := f.mpstatsFirst(hc)
	return cl, err
}

// mpstatsFirst is mpstatsConnClient with the connection's id: по нему ключи кэшей.
func (f *folderSettings) mpstatsFirst(hc *http.Client) (mpstats.Client, string, error) {
	for _, c := range f.snapshot().cfg.Connections {
		if c.Kind == desktopconfig.ConnMPStats && c.Enabled {
			cl, err := f.mpstatsByID(c.ID, hc)
			return cl, c.ID, err
		}
	}
	return mpstats.Client{}, "", errors.New("подключения MPStats нет — добавьте ключ в Настройках → Подключения → + MPStats")
}

// MarketsNichesMPStats shows the niches of category path for days from..to (ГГГГ-ММ-ДД).
func (a *App) MarketsNichesMPStats(path, from, to string) (nicheTable, error) {
	wait := 3 * time.Minute
	if mpstatsPath(path) == "" {
		wait = 6 * time.Minute // все категории: 34 верхних — около 70 запросов (замер 14.11: 3 минуты не хватило)
	}
	ctx, cancel := context.WithTimeout(a.ctx, wait)
	defer cancel()
	return a.settings.nichesMPStats(ctx, nil, path, from, to, time.Now())
}

// MarketsMPStatsCategories returns the paths of Ozon categories for the category field.
func (a *App) MarketsMPStatsCategories() ([]string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	cl, err := a.settings.mpstatsConnClient(nil)
	if err != nil {
		return nil, err
	}
	paths, err := cl.Categories(ctx)
	if err != nil {
		return nil, errors.New(mpstatsMessage(err))
	}
	return paths, nil
}

// MarketsNichesMPStatsSeason is MarketsNichesMPStats with each niche's year by
// months — вторым вызовом: таблица видна сразу, сезон догружается (до минуты).
func (a *App) MarketsNichesMPStatsSeason(path, from, to string) (nicheTable, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	return a.settings.nichesMPStatsSeason(ctx, nil, path, from, to, time.Now())
}

// nichesMPStats reads the subcategories of path for the days and a year earlier.
func (f *folderSettings) nichesMPStats(ctx context.Context, hc *http.Client, path, from, to string, now time.Time) (nicheTable, error) {
	return f.nichesRead(ctx, hc, path, from, to, now, false)
}

// nichesMPStatsSeason is nichesMPStats with the niches' year by months.
func (f *folderSettings) nichesMPStatsSeason(ctx context.Context, hc *http.Client, path, from, to string, now time.Time) (nicheTable, error) {
	return f.nichesRead(ctx, hc, path, from, to, now, true)
}

func (f *folderSettings) nichesRead(ctx context.Context, hc *http.Client, path, from, to string, now time.Time, season bool) (nicheTable, error) {
	path = mpstatsPath(path)
	d1, d2, err := mpstatsDays(from, to)
	if err != nil {
		return nicheTable{}, err
	}
	cl, id, err := f.mpstatsFirst(hc)
	if err != nil {
		return nicheTable{}, err
	}
	days := d1.Format(time.DateOnly) + "|" + d2.Format(time.DateOnly)
	if path == "" { // все категории: верхние с цифрами, дальше — «Внутрь»
		k := id + "|" + days // ключ другого аккаунта — свой ответ (ревью 14.11)
		if v, ok := f.mpstatsTop.get(k, now); ok {
			return v, nil
		}
		v, err := topCategories(ctx, cl, d1, d2, now)
		if err == nil && len(v.Problems) == 0 { // неполный (тайм-аут части категорий) не запоминаем
			f.mpstatsTop.put(k, v, now)
		}
		return v, err
	}
	// Догрузка сезона идет сразу за показом: подкатегории и топ-10 — из памяти,
	// иначе тариф тратился бы вдвое (ревью 14.11).
	k := id + "|" + path + "|" + days
	b, ok := f.mpstatsTable.get(k, now)
	if !ok {
		if b, err = readNicheBase(ctx, cl, path, d1, d2); err != nil {
			return nicheTable{}, err
		}
		if b.missed == 0 && b.prevErr == nil { // неполную не запоминаем: следующий показ спросит еще раз
			f.mpstatsTable.put(k, b, now)
		}
	}
	return nichesOf(ctx, cl, b, path, d1, d2, now, season)
}

// nicheBase — таблица ниш без сезона: подкатегории сейчас и год назад, топ-10.
type nicheBase struct {
	parent    string
	cur, prev []markets.NicheSale
	prevErr   error
	missed    int
	why       string
}

// nichesWith reads the niches of path through client cl.
func nichesWith(ctx context.Context, cl mpstats.Client, path string, d1, d2, now time.Time, season bool) (nicheTable, error) {
	b, err := readNicheBase(ctx, cl, path, d1, d2)
	if err != nil {
		return nicheTable{}, err
	}
	return nichesOf(ctx, cl, b, path, d1, d2, now, season)
}

// readNicheBase reads the subcategories of path and their top-10 revenue.
func readNicheBase(ctx context.Context, cl mpstats.Client, path string, d1, d2 time.Time) (nicheBase, error) {
	var subs [2][]mpstats.Subcategory
	var self [2]mpstats.Subcategory
	var errs [2]error
	var wg sync.WaitGroup
	for i, back := range []int{0, -1} { // сейчас и те же дни год назад — для роста к году
		wg.Go(func() {
			subs[i], self[i], errs[i] = cl.Subcategories(ctx, path, d1.AddDate(back, 0, 0), d2.AddDate(back, 0, 0))
		})
	}
	wg.Wait()
	if errs[0] != nil { // год назад — только для роста: без него ниши есть (ревью #606)
		return nicheBase{}, errors.New(mpstatsMessage(errs[0]))
	}
	parent, subs, err := leafAsNiche(path, subs, self)
	if err != nil {
		return nicheBase{}, err
	}
	sale := func(ss []mpstats.Subcategory) []markets.NicheSale {
		out := make([]markets.NicheSale, 0, len(ss))
		for _, s := range ss {
			out = append(out, markets.NicheSale{Name: s.Name, Sales: s.Sales, Revenue: s.Revenue, Sellers: s.Sellers})
		}
		return out
	}
	b := nicheBase{parent: parent, cur: sale(subs[0]), prev: sale(subs[1]), prevErr: errs[1]}
	b.missed, b.why = topTen(ctx, cl, parent, d1, d2, b.cur)
	return b, nil
}

// nichesOf builds the niche table from b; season — догрузить год по месяцам.
func nichesOf(ctx context.Context, cl mpstats.Client, b nicheBase, path string, d1, d2, now time.Time, season bool) (nicheTable, error) {
	parent := b.parent
	cur := slices.Clone(b.cur) // сезон дописывает месяцы: запомненная база остается без них
	var noSeason int
	var seasonWhy string
	if season {
		noSeason, seasonWhy = seasons(ctx, cl, parent, now, cur)
	}
	tb, err := markets.MPStatsNiches(parent, cur, b.prev, int(d2.Sub(d1).Hours()/24)+1)
	if err != nil {
		return nicheTable{}, err
	}
	if b.prevErr != nil {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: "рост к году не посчитан: " + mpstatsMessage(b.prevErr)})
	}
	if b.missed > 0 {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: fmt.Sprintf("у %d ниш нет доли топ-10 и оценки: %s", b.missed, b.why)})
	}
	if noSeason > 0 {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: fmt.Sprintf("у %d ниш нет сезона: %s", noSeason, seasonWhy)})
	}
	v := nicheViewOf(tb, "mpstats:"+path, "MPStats · "+path, now)
	v.Leaf = parent != path // leafAsNiche подставила саму нишу
	return v, nil
}

// topCategories is the top Ozon categories for days d1..d2 with growth to a
// year earlier: у каждой — ее строка «Текущая категория»; запрос на категорию
// и год, до четырех сразу (≈ 25 категорий, около минуты).
// ponytail: верхние категории без доли топ-10 и сезона — их оценка частичная;
// путь — догружать их, как у ниш, если понадобится.
func topCategories(ctx context.Context, cl mpstats.Client, d1, d2, now time.Time) (nicheTable, error) {
	paths, err := cl.Categories(ctx)
	if err != nil {
		return nicheTable{}, errors.New(mpstatsMessage(err))
	}
	tops := topNames(paths)
	cur, prev, errs, prevErrs := topSales(ctx, cl, tops, d1, d2)
	var got []markets.NicheSale
	var missed, noPrev int
	var cause, prevCause error
	for i := range tops {
		if errs[i] != nil {
			missed++
			cause = realCause(cause, errs[i])
			continue
		}
		got = append(got, cur[i])
		if prevErrs[i] != nil {
			noPrev++
			prevCause = cmp.Or(prevCause, prevErrs[i])
		}
	}
	if len(got) == 0 && cause != nil {
		return nicheTable{}, errors.New(mpstatsMessage(cause))
	}
	tb, err := markets.MPStatsNiches("", got, prev, int(d2.Sub(d1).Hours()/24)+1)
	if err != nil {
		return nicheTable{}, err
	}
	if missed > 0 {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: fmt.Sprintf("%d категорий не загружены: %s", missed, mpstatsMessage(cause))})
	}
	if noPrev > 0 {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: fmt.Sprintf("у %d категорий рост к году не посчитан: %s", noPrev, mpstatsMessage(prevCause))})
	}
	return nicheViewOf(tb, "mpstats:", "MPStats · все категории", now), nil
}

// topNames are the first segments of paths, по порядку, без повторов.
func topNames(paths []string) []string {
	var tops []string
	for _, p := range paths {
		top, _, _ := strings.Cut(p, "/")
		if top != "" && !slices.Contains(tops, top) {
			tops = append(tops, top)
		}
	}
	return tops
}

// topSales reads each top category's own row now and a year earlier, до
// четырех категорий сразу; errs — отказ «сейчас» по каждой.
// errs — отказ «сейчас» по категории, prevErrs — год назад (тогда нет роста).
// Исчерпанный лимит и отказ ключа останавливают остальные запросы (ревью 14.11).
func topSales(ctx context.Context, cl mpstats.Client, tops []string, d1, d2 time.Time) (cur, prev []markets.NicheSale, errs, prevErrs []error) {
	cur, prev = make([]markets.NicheSale, len(tops)), make([]markets.NicheSale, len(tops))
	errs, prevErrs = make([]error, len(tops)), make([]error, len(tops))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, top := range tops {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			for k, out := range [][]markets.NicheSale{cur, prev} {
				_, self, err := cl.Subcategories(ctx, top, d1.AddDate(-k, 0, 0), d2.AddDate(-k, 0, 0))
				if stopsAll(err) {
					cancel()
				}
				if err != nil && k == 0 {
					errs[i] = err
					return // без «сейчас» год назад не нужен
				}
				if err != nil {
					prevErrs[i] = err
					continue
				}
				out[i] = markets.NicheSale{Name: top, Sales: self.Sales, Revenue: self.Revenue, Sellers: self.Sellers}
			}
		})
	}
	wg.Wait()
	return cur, prev, errs, prevErrs
}

// realCause keeps the first real reason: отмена — следствие остановки, а не причина.
func realCause(cause, err error) error {
	if cause == nil || (errors.Is(cause, context.Canceled) && !errors.Is(err, context.Canceled)) {
		return err
	}
	return cause
}

// stopsAll: исчерпанный лимит и отказ ключа — остальные запросы бесполезны.
func stopsAll(err error) bool {
	return errors.Is(err, mpstats.ErrQuota) || errors.Is(err, mpstats.ErrUnauthorized)
}

// seasons fills each niche's sales by months: 12 полных месяцев до текущего,
// запрос на нишу (≈ 13 с, замер 14.11), до четырех сразу; отказ ключа и лимит
// останавливают остальные. missed — сколько ниш без сезона, why — причина.
func seasons(ctx context.Context, cl mpstats.Client, parent string, now time.Time, ns []markets.NicheSale) (missed int, why string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	from, to := first.AddDate(-1, 0, 0), first.AddDate(0, 0, -1)
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var cause error
	for i := range ns {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			ms, err := cl.CategoryMonths(ctx, childPath(parent, ns[i].Name), from, to)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				missed++
				if stopsAll(err) { // при любой прежней причине (ревью 14.11)
					cancel()
				}
				if cause == nil && !errors.Is(err, context.Canceled) {
					cause = err
				}
				return
			}
			for _, m := range ms {
				ns[i].Months = append(ns[i].Months, domain.MonthSales{Month: m.Month.Month(), Year: m.Month.Year(), Value: m.Sales})
			}
		})
	}
	wg.Wait()
	if cause != nil {
		why = mpstatsMessage(cause)
	}
	return missed, why
}

// topTen fills the top-10 revenue of each niche: запрос на нишу, до четырех
// сразу (около 5 с на нишу, замер 13.11). Исчерпанный лимит и отказ ключа
// останавливают остальные запросы (ревью #606); missed — сколько ниш без
// ответа, why — причина словами.
func topTen(ctx context.Context, cl mpstats.Client, parent string, from, to time.Time, ns []markets.NicheSale) (missed int, why string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var cause error // первая настоящая причина; отмена — следствие остановки
	for i := range ns {
		if ns[i].Revenue <= 0 {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			v, err := cl.TopSellersRevenue(ctx, childPath(parent, ns[i].Name), from, to, 10)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ns[i].Top10Revenue = v
				return
			}
			missed++
			if stopsAll(err) {
				cancel()
			}
			cause = realCause(cause, err)
		})
	}
	wg.Wait()
	if cause != nil {
		why = mpstatsMessage(cause)
	}
	return missed, why
}

// mpstatsDays reads the period «ГГГГ-ММ-ДД».
func mpstatsDays(from, to string) (d1, d2 time.Time, err error) {
	d1, err1 := time.Parse(time.DateOnly, from)
	d2, err2 := time.Parse(time.DateOnly, to)
	switch {
	case err1 != nil || err2 != nil:
		return d1, d2, fmt.Errorf("даты периода не прочитаны: %q – %q", from, to)
	case d2.Before(d1):
		return d1, d2, markets.ErrOzonPeriod
	}
	return d1, d2, nil
}

// childPath is the MPStats path of niche name inside parent; у верхней
// категории parent пустой — путь без «/» в начале (ревью 14.11).
func childPath(parent, name string) string { return strings.TrimPrefix(parent+"/"+name, "/") }

// leafAsNiche: у конечной ниши подкатегорий нет — в таблице она сама (живая
// проверка 13.11, «Пижамы женские»); parent — путь, в котором она лежит.
func leafAsNiche(path string, subs [2][]mpstats.Subcategory, self [2]mpstats.Subcategory) (string, [2][]mpstats.Subcategory, error) {
	if len(subs[0]) > 0 {
		return path, subs, nil
	}
	if self[0].Sales <= 0 && self[0].Revenue <= 0 {
		return "", subs, fmt.Errorf("в MPStats нет продаж категории «%s» за эти дни — проверьте путь (окно подскажет пути)", path)
	}
	i := strings.LastIndex(path, "/")
	self[0].Name, self[1].Name = path[i+1:], path[i+1:]
	return path[:max(i, 0)], [2][]mpstats.Subcategory{{self[0]}, {self[1]}}, nil
}
