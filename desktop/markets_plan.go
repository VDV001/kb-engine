package main

import (
	"context"
	"math"
	"net/http"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/adapter/planxlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// planInput — ввод «Плана» в окне: комиссия и логистика в процентах.
type planInput struct {
	Stock      float64 `json:"stock"`
	Cost       float64 `json:"cost"`
	Price      float64 `json:"price"`
	Commission float64 `json:"commission"`
	Logistics  float64 `json:"logistics"`
	LeadDays   int     `json:"leadDays"`
}

type planWeek struct {
	From     string  `json:"from"`
	Days     int     `json:"days"`
	Demand   float64 `json:"demand"`
	Sell     float64 `json:"sell"`
	StockEnd float64 `json:"stockEnd"`
	Price    float64 `json:"price"`
	Profit   float64 `json:"profit"`
	Peak     bool    `json:"peak"`
	Dip      bool    `json:"dip"`
	Short    bool    `json:"short"`
}

type planView struct {
	Weeks        []planWeek `json:"weeks"`
	Sell         float64    `json:"sell"`
	Profit       float64    `json:"profit"`
	SupplyQty    float64    `json:"supplyQty"`
	SupplyBy     string     `json:"supplyBy"`
	ShortForPeak bool       `json:"shortForPeak"`
	Peak         string     `json:"peak"`
}

// buildPlan forecasts from the three sources and plans from the forecast.
func (f *folderSettings) buildPlan(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput) (domain.Plan, error) {
	series, _, err := f.forecastInputs(ctx, base, hc, last, now, own, today)
	if err != nil {
		return domain.Plan{}, err
	}
	fc, err := domain.NewForecast(series[0], series[1], series[2])
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.NewPlan(fc, domain.PlanInput{Stock: in.Stock, Cost: in.Cost, Price: in.Price,
		Commission: in.Commission / 100, Logistics: in.Logistics / 100, LeadDays: in.LeadDays})
}

// plan builds the «План» view: недели, итог, поставка.
func (f *folderSettings) plan(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput) (planView, error) {
	p, err := f.buildPlan(ctx, base, hc, last, now, own, today, in)
	if err != nil {
		return planView{}, err
	}
	r := func(x float64) float64 { return math.Round(x) }
	v := planView{Weeks: []planWeek{}, Sell: r(p.Sell()), Profit: r(p.Profit()), ShortForPeak: p.ShortForPeak()}
	qty, by := p.Supply()
	v.SupplyQty = r(qty)
	if !by.IsZero() {
		v.SupplyBy = by.Format("02.01.2006")
	}
	for _, w := range p.Weeks() {
		v.Weeks = append(v.Weeks, planWeek{From: w.From.Format("02.01.2006"), Days: w.Days, Demand: r(w.Demand), Sell: r(w.Sell), StockEnd: r(w.StockEnd),
			Price: r(w.Price), Profit: r(w.Profit), Peak: w.Peak, Dip: w.Dip, Short: w.Short})
	}
	v.Peak = p.Peak().Format("02.01.2006")
	return v, nil
}

// planExcel writes the plan to path.
func (f *folderSettings) planExcel(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput, path string) error {
	p, err := f.buildPlan(ctx, base, hc, last, now, own, today, in)
	if err != nil {
		return err
	}
	return planxlsx.Write(path, p)
}

// MarketsPlan builds the plan from the forecast's three sources.
func (a *App) MarketsPlan(lastYear, nicheNow, own string, in planInput) (planView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.plan(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, own, time.Now(), in)
}

// MarketsPlanExcel asks where to save and writes the plan; "" — человек отменил.
func (a *App) MarketsPlanExcel(lastYear, nicheNow, own string, in planInput) (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Сохранить план", DefaultFilename: "План продаж " + time.Now().Format("2006-01-02") + ".xlsx",
		Filters: []runtime.FileFilter{{DisplayName: "Excel", Pattern: "*.xlsx"}}})
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) != ".xlsx" {
		path += ".xlsx"
	}
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return path, a.settings.planExcel(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, own, time.Now(), in, path)
}
