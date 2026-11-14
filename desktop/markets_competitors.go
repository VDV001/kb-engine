package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// competitorsTop — сколько продавцов ниши показывать.
const competitorsTop = 20

// competitorRow — продавец ниши для окна; доли — как 0,25.
type competitorRow struct {
	Name        string  `json:"name"`
	Items       int     `json:"items"`
	Sales       float64 `json:"sales"`
	Revenue     float64 `json:"revenue"`
	AvgPrice    float64 `json:"avgPrice"`
	Rating      float64 `json:"rating"`
	Share       float64 `json:"share"`
	SalesChange float64 `json:"salesChange"`
	PriceChange float64 `json:"priceChange"`
	ItemsAdded  int     `json:"itemsAdded"`
	New         bool    `json:"new"`
}

// competitorsView — топ продавцов ниши за Period и изменения к Prev.
type competitorsView struct {
	Path   string          `json:"path"`
	Period string          `json:"period"`
	Prev   string          `json:"prev"`
	Rows   []competitorRow `json:"rows"`
	Note   string          `json:"note"` // прошлый период не прочитан — изменений нет
}

// MarketsCompetitors shows the top sellers of niche path for days to yesterday.
func (a *App) MarketsCompetitors(path string, days int) (competitorsView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return a.settings.competitors(ctx, nil, path, days, time.Now())
}

// competitors reads the sellers of niche path for days to yesterday and the
// same days before (#613): два запроса MPStats, сразу оба.
func (f *folderSettings) competitors(ctx context.Context, hc *http.Client, path string, days int, now time.Time) (competitorsView, error) {
	path = mpstatsPath(path)
	if path == "" {
		return competitorsView{}, errors.New("выберите нишу: путь категории Ozon, как в MPStats")
	}
	days = min(max(days, 7), 90)
	cl, _, err := f.mpstatsFirst(hc)
	if err != nil {
		return competitorsView{}, err
	}
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	from := to.AddDate(0, 0, 1-days)
	pTo, pFrom := from.AddDate(0, 0, -1), from.AddDate(0, 0, -days)
	var cur, prev []markets.SellerStat
	var curErr, prevErr error
	var wg sync.WaitGroup
	wg.Go(func() { cur, curErr = cl.Sellers(ctx, path, from, to) })
	wg.Go(func() { prev, prevErr = cl.Sellers(ctx, path, pFrom, pTo) })
	wg.Wait()
	if curErr != nil {
		return competitorsView{}, errors.New(mpstatsMessage(curErr))
	}
	day := func(t time.Time) string { return t.Format("02.01.2006") }
	v := competitorsView{Path: path, Period: day(from) + " – " + day(to), Prev: day(pFrom) + " – " + day(pTo), Rows: []competitorRow{}}
	if prevErr != nil {
		prev = nil
		v.Note = "изменений нет: прошлый период не прочитан — " + mpstatsMessage(prevErr)
	}
	for _, r := range markets.Competitors(cur, prev, competitorsTop) {
		v.Rows = append(v.Rows, competitorRow{Name: r.Name, Items: r.Items, Sales: r.Sales, Revenue: r.Revenue, AvgPrice: r.AvgPrice, Rating: r.Rating,
			Share: r.Share, SalesChange: r.SalesChange, PriceChange: r.PriceChange, ItemsAdded: r.ItemsAdded, New: r.New && prevErr == nil})
	}
	return v, nil
}
