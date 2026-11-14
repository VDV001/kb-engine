package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/adapter/planxlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
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

// planDay — день плана для «План по дням».
type planDay struct {
	Day      string  `json:"day"`
	Demand   float64 `json:"demand"`
	Sell     float64 `json:"sell"`
	StockEnd float64 `json:"stockEnd"`
	Price    float64 `json:"price"`
	Profit   float64 `json:"profit"`
}

type planView struct {
	Weeks        []planWeek `json:"weeks"`
	Days         []planDay  `json:"days"`
	Demand       float64    `json:"demand"` // спрос прогноза за весь план
	Stock        float64    `json:"stock"`
	Out          string     `json:"out"` // день, когда остатка не хватит; пусто — хватит
	Sell         float64    `json:"sell"`
	Profit       float64    `json:"profit"`
	SupplyQty    float64    `json:"supplyQty"`
	SupplyBy     string     `json:"supplyBy"`
	SupplyLate   bool       `json:"supplyLate"` // срок прошел — везти сейчас
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
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC) // дни прогноза — полночь UTC
	return domain.NewPlan(fc, domain.PlanInput{Stock: in.Stock, Cost: in.Cost, Price: in.Price,
		Commission: in.Commission / 100, Logistics: in.Logistics / 100, LeadDays: in.LeadDays, From: day})
}

// plan builds the «План» view: недели, итог, поставка.
func (f *folderSettings) plan(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput) (planView, error) {
	p, err := f.buildPlan(ctx, base, hc, last, now, own, today, in)
	if err != nil {
		return planView{}, err
	}
	r := func(x float64) float64 { return math.Round(x) }
	f.planMu.Lock()
	f.lastPlan = &p // «Скачать Excel» пишет ровно этот план (ревью #599)
	f.planMu.Unlock()
	v := planView{Weeks: []planWeek{}, Days: []planDay{}, Sell: r(p.Sell()), Profit: r(p.Profit()), ShortForPeak: p.ShortForPeak(), SupplyLate: p.SupplyLate(),
		Demand: r(p.Demand()), Stock: in.Stock}
	if out := p.OutOfStock(); !out.IsZero() {
		v.Out = out.Format("02.01.2006")
	}
	for _, d := range p.Days() {
		v.Days = append(v.Days, planDay{Day: d.Day.Format("02.01.2006"), Demand: r(d.Demand), Sell: r(d.Sell), StockEnd: r(d.StockEnd), Price: r(d.Price), Profit: r(d.Profit)})
	}
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

// errNoPlan — Excel просят до плана.
var errNoPlan = errors.New("сначала постройте план")

// planExcel writes the last built plan to path.
func (f *folderSettings) planExcel(path string) error {
	f.planMu.Lock()
	p := f.lastPlan
	f.planMu.Unlock()
	if p == nil {
		return errNoPlan
	}
	return planxlsx.Write(path, *p)
}

// xlsxPath adds .xlsx when the name has no such extension, в любом регистре.
func xlsxPath(p string) string {
	if strings.EqualFold(filepath.Ext(p), ".xlsx") {
		return p
	}
	return p + ".xlsx"
}

// planStockItem — товар кабинета в списке выбора «Плана».
type planStockItem struct {
	SKU     string  `json:"sku"`
	Article string  `json:"article"`
	Name    string  `json:"name"`
	Qty     float64 `json:"qty"`
}

// planStock — остаток для «Плана»: по выбранным товарам или, без выбора, по
// всему кабинету, как и свои продажи в прогнозе (решения владельца 14.11).
// Items — весь кабинет при любом выборе: из него выбирают.
type planStock struct {
	Name     string          `json:"name"`
	Qty      float64         `json:"qty"`
	Products int             `json:"products"`
	InStock  int             `json:"inStock"`
	Items    []planStockItem `json:"items"`
}

// stockTotal sums stock to sell; резерв больше наличия у одного товара
// не уменьшает остаток других.
func stockTotal(st []markets.OzonStock) (qty float64, inStock int) {
	for _, s := range st {
		if s.Qty > 0 {
			qty += s.Qty
			inStock++
		}
	}
	return qty, inStock
}

// ozonPlanStock reads the stock of cabinet id now; skus — выбранные товары, nil — все.
func (f *folderSettings) ozonPlanStock(ctx context.Context, base string, hc *http.Client, id string, skus []string) (planStock, error) {
	cl, c, err := f.ozonCabinet(id, base, hc)
	if err != nil {
		return planStock{}, err
	}
	st, err := cl.Stocks(ctx)
	if err != nil {
		return planStock{}, ozonErr(c.Name, err)
	}
	v := planStock{Name: c.Name, Items: make([]planStockItem, 0, len(st))}
	picked := st
	if len(skus) > 0 {
		picked = nil
	}
	for _, s := range st {
		v.Items = append(v.Items, planStockItem{SKU: s.SKU, Article: s.Article, Name: s.Name, Qty: s.Qty})
		if len(skus) > 0 && slices.Contains(skus, s.SKU) {
			picked = append(picked, s)
		}
	}
	v.Qty, v.InStock = stockTotal(picked)
	v.Products = len(picked)
	return v, nil
}

// MarketsPlanStock is the stock of own sales source own («ozon:<id>» или
// «ozon:<id>#SKU,SKU») for the «План» field.
func (a *App) MarketsPlanStock(own string) (planStock, error) {
	id, skus, ok, err := ozonOwn(own)
	if err != nil {
		return planStock{}, err
	}
	if !ok {
		return planStock{}, errors.New("остаток подставляется только из кабинета Ozon")
	}
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.ozonPlanStock(ctx, ozon.DefaultURL, nil, id, skus)
}

// MarketsPlan builds the plan from the forecast's three sources.
func (a *App) MarketsPlan(lastYear, nicheNow, own string, in planInput) (planView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.plan(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, own, time.Now(), in)
}

// MarketsPlanExcel asks where to save and writes the plan on screen; "" — отменил.
// Ввод не нужен: пишется построенный план, а не пересчитанный (ревью #599).
func (a *App) MarketsPlanExcel() (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Сохранить план", DefaultFilename: "План продаж " + time.Now().Format("2006-01-02") + ".xlsx",
		Filters: []runtime.FileFilter{{DisplayName: "Excel", Pattern: "*.xlsx"}}})
	if err != nil || path == "" {
		return "", err
	}
	if full := xlsxPath(path); full != path {
		// Диалог подтвердил имя без расширения: чужой файл с .xlsx молча не затираем (ревью #599).
		if _, err := os.Stat(full); err == nil {
			return "", fmt.Errorf("файл %s уже есть — выберите другое имя", filepath.Base(full))
		}
		path = full
	}
	return path, a.settings.planExcel(path)
}
