package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	key := src + "|" + today.Format(time.DateOnly)
	if e, ok := f.mpstatsNiche.get(key, time.Now()); ok {
		return e.series, e.files, nil
	}
	cl, path, err := f.mpstatsSource(src, hc)
	if err != nil {
		return series, nil, err
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
	f.mpstatsNiche.put(key, nicheEntry{at: time.Now(), series: series, files: files[:]})
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
	for _, c := range f.snapshot().cfg.Connections {
		if c.Kind == desktopconfig.ConnMPStats && c.Enabled {
			return f.mpstatsByID(c.ID, hc)
		}
	}
	return mpstats.Client{}, errors.New("подключения MPStats нет — добавьте ключ в Настройках → Подключения → + MPStats")
}

// MarketsNichesMPStats shows the niches of category path for days from..to (ГГГГ-ММ-ДД).
func (a *App) MarketsNichesMPStats(path, from, to string) (nicheTable, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
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

// nichesMPStats reads the subcategories of path for the days and a year earlier.
func (f *folderSettings) nichesMPStats(ctx context.Context, hc *http.Client, path, from, to string, now time.Time) (nicheTable, error) {
	path = mpstatsPath(path)
	if path == "" {
		return nicheTable{}, errors.New("впишите категорию Ozon, например «Одежда/Мужская одежда»")
	}
	d1, d2, err := mpstatsDays(from, to)
	if err != nil {
		return nicheTable{}, err
	}
	cl, err := f.mpstatsConnClient(hc)
	if err != nil {
		return nicheTable{}, err
	}
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
		return nicheTable{}, errors.New(mpstatsMessage(errs[0]))
	}
	parent, subs, err := leafAsNiche(path, subs, self)
	if err != nil {
		return nicheTable{}, err
	}
	sale := func(ss []mpstats.Subcategory) []markets.NicheSale {
		out := make([]markets.NicheSale, 0, len(ss))
		for _, s := range ss {
			out = append(out, markets.NicheSale{Name: s.Name, Sales: s.Sales, Revenue: s.Revenue, Sellers: s.Sellers})
		}
		return out
	}
	cur := sale(subs[0])
	missed, why := topTen(ctx, cl, parent, d1, d2, cur)
	tb, err := markets.MPStatsNiches(parent, cur, sale(subs[1]), int(d2.Sub(d1).Hours()/24)+1)
	if err != nil {
		return nicheTable{}, err
	}
	if errs[1] != nil {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: "рост к году не посчитан: " + mpstatsMessage(errs[1])})
	}
	if missed > 0 {
		tb.Problems = append(tb.Problems, markets.Problem{Reason: fmt.Sprintf("у %d ниш нет доли топ-10 и оценки: %s", missed, why)})
	}
	return nicheViewOf(tb, "mpstats:"+path, "MPStats · "+path, now), nil
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
			v, err := cl.TopSellersRevenue(ctx, parent+"/"+ns[i].Name, from, to, 10)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ns[i].Top10Revenue = v
				return
			}
			missed++
			if errors.Is(err, mpstats.ErrQuota) || errors.Is(err, mpstats.ErrUnauthorized) {
				cancel()
			}
			if cause == nil || (errors.Is(cause, context.Canceled) && !errors.Is(err, context.Canceled)) {
				cause = err
			}
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
