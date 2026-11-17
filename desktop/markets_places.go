package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// placeRow — место товара в выдаче по одному запросу (#670).
type placeRow struct {
	Query   string `json:"query"`
	Count   int    `json:"count"`   // запросов за период
	Avg     int    `json:"avg"`     // среднее место
	Last    int    `json:"last"`    // последнее место; 0 — не был в выдаче
	LastDay string `json:"lastDay"` // ГГГГ-ММ-ДД последнего места
	Seen    int    `json:"seen"`    // дней в выдаче
	Days    int    `json:"days"`    // дней в периоде
}

// placeStats — видимость в поиске за период и за такой же период до него.
type placeStats struct {
	Words       int `json:"words"`
	WordsBefore int `json:"wordsBefore"`
	Avg         int `json:"avg"`
	AvgBefore   int `json:"avgBefore"`
}

// placesView — «Место в выдаче» карточки (#670): запросы, по которым товар в
// выдаче Ozon, с местом; без запросов — сравнение с прошлым периодом.
type placesView struct {
	SKU    string      `json:"sku"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Total  int         `json:"total"` // запросов всего; в Rows — первые placesShown
	Rows   []placeRow  `json:"rows"`
	Stats  *placeStats `json:"stats"`
	Calls  int         `json:"calls"` // запросов тарифа MPStats на эту проверку
	Source string      `json:"source"`
}

// placesShown — сколько запросов показать: дальше редкие, место по ним шумное.
const placesShown = 30

// moscow — сутки MPStats: «вчера» — по Москве.
var moscow = time.FixedZone("MSK", 3*60*60)

// searchPeriod is MPStats's default span: вчера по Москве и 30 дней до него.
func searchPeriod(now time.Time) (from, to time.Time) {
	d := now.In(moscow)
	to = time.Date(d.Year(), d.Month(), d.Day()-1, 0, 0, 0, 0, moscow)
	return to.AddDate(0, 0, -30), to
}

// MarketsPlaces shows where card sku is in Ozon search; fresh — мимо памяти.
func (a *App) MarketsPlaces(sku string, fresh bool) (placesView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return a.settings.placesCheck(ctx, nil, sku, fresh, time.Now())
}

// ownQueries reads the card's own queries: память «Названия», своя память или
// отчет keywords (calls — 1). Пусто — товар не находят ни по одному запросу.
func (f *folderSettings) ownQueries(ctx context.Context, m mpstats.MCP, ck string, id int64, fresh bool, now time.Time) (cardPart, int, error) {
	if !fresh {
		if cp, ok := f.cardParts.get(ck, now); ok {
			return cp, 0, nil
		}
		if cp, ok := f.ownParts.get(ck, now); ok {
			return cp, 0, nil
		}
	}
	from, to := searchPeriod(now)
	own, err := m.CardQueries(ctx, id, from, to)
	if err != nil && !errors.Is(err, mpstats.ErrNotFound) {
		return cardPart{}, 1, errors.New("запросы товара не прочитаны — " + mpstatsMessage(err))
	}
	cp := cardPart{own: own, from: from}
	f.ownParts.put(ck, cp, now)
	return cp, 1, nil
}

// placesCheck reads the card's own queries: из памяти «Названия» и
// «Хештегов», если они уже читали, иначе один отчет keywords; пусто — еще
// search_stats, чтобы сказать, был ли товар в выдаче раньше.
func (f *folderSettings) placesCheck(ctx context.Context, hc *http.Client, sku string, fresh bool, now time.Time) (placesView, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return placesView{}, errors.New("место в выдаче смотрится у товара Ozon с числовым SKU")
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return placesView{}, err
	}
	m := mpstats.MCP{URL: f.mcpBase(), Token: cl.Token, HTTP: hc}
	ck := connID + "|" + strconv.FormatInt(id, 10)
	v := placesView{SKU: strconv.FormatInt(id, 10), Rows: []placeRow{}, Source: "MPStats, отчет ozon_sku keywords"}
	cp, calls, err := f.ownQueries(ctx, m, ck, id, fresh, now)
	if err != nil {
		return placesView{}, err
	}
	v.Calls += calls
	v.From, v.To = cp.from.Format(time.DateOnly), cp.from.AddDate(0, 0, 30).Format(time.DateOnly)
	v.Total = len(cp.own)
	for _, p := range markets.SearchPlaces(cp.own, cp.from, placesShown) {
		r := placeRow{Query: p.Query, Count: p.Count, Avg: p.Avg, Last: p.Last, Seen: p.Seen, Days: p.Days}
		if !p.LastDay.IsZero() {
			r.LastDay = p.LastDay.Format(time.DateOnly)
		}
		v.Rows = append(v.Rows, r)
	}
	if v.Total > 0 {
		return v, nil
	}
	st, err := m.SearchStats(ctx, id, cp.from, cp.from.AddDate(0, 0, 30))
	v.Calls++
	if err != nil {
		return v, nil // запросов нет и так; сравнения с прошлым не будет
	}
	v.Stats = &placeStats{Words: st.Words, WordsBefore: st.WordsBefore, Avg: st.Avg, AvgBefore: st.AvgBefore}
	v.Source += " и search_stats"
	return v, nil
}
