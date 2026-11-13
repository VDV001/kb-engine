package main

import (
	"math"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/adapter/tablefile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Маркеты → Магазин» (#580): отчет Ozon «По товарам» своего магазина —
// выкуп, отмены, остатки и проблемы по каждому товару. Файл окно только читает.

type shopRow struct {
	Name       string   `json:"name"`
	SKU        string   `json:"sku"`
	Article    string   `json:"article"`
	Scheme     string   `json:"scheme"`
	ABC        string   `json:"abc"`
	PriceIndex string   `json:"priceIndex"`
	OrderedSum float64  `json:"orderedSum"`
	BoughtSum  float64  `json:"boughtSum"` // выкуплено на сумму; 0 — источник не знает
	Ordered    float64  `json:"ordered"`
	Delivered  float64  `json:"delivered"`
	BoughtOut  float64  `json:"boughtOut"`
	Cancelled  float64  `json:"cancelled"`
	Refused    float64  `json:"refused"` // отказ при вручении, часть отмен (API Ozon)
	Returned   float64  `json:"returned"`
	Buyout     float64  `json:"buyout"`
	Cancel     float64  `json:"cancel"`
	Refuse     float64  `json:"refuse"`
	Return     float64  `json:"return"`
	AvgPrice   float64  `json:"avgPrice"`
	DRR        float64  `json:"drr"`
	Stock      float64  `json:"stock"`
	SupplyQty  float64  `json:"supplyQty"`
	Urgent     bool     `json:"urgent"`
	DaysOut    int      `json:"daysOut"`
	DaysOutOf  int      `json:"daysOutOf"`
	PromoDays  int      `json:"promoDays"`
	Rating     float64  `json:"rating"`
	Reviews    float64  `json:"reviews"`
	Issues     []string `json:"issues"`
	// Деньги товара из начислений кабинета (#580); nil — у отчета-файла их нет.
	Money *rowMoney `json:"money"`
	// OnAccount и Profit — для сортировки таблицы; Profit — только с себестоимостью.
	OnAccount float64  `json:"onAccount"`
	Profit    *float64 `json:"profit"`
}

// rowMoney — экономика товара за период до рекламы, в рублях.
type rowMoney struct {
	Units      int     `json:"units"` // продано штук за вычетом возвратов
	Sales      float64 `json:"sales"`
	Commission float64 `json:"commission"`
	Delivery   float64 `json:"delivery"`
	Partners   float64 `json:"partners"`
	Other      float64 `json:"other"`
	Total      float64 `json:"total"`
	UnitCost   float64 `json:"unitCost"`
	HasCost    bool    `json:"hasCost"`
	Cost       float64 `json:"cost"`
}

// costImport — что прочитал файл себестоимости.
type costImport struct {
	Read     int                `json:"read"`
	Problems []nicheProblem     `json:"problems"`
	Costs    map[string]float64 `json:"costs"` // принятые: артикул → рубли
}

type shopView struct {
	File       string            `json:"file"`
	Path       string            `json:"path"`
	Period     string            `json:"period"`
	PeriodDays int               `json:"periodDays"`
	Total      shopRow           `json:"total"`
	Products   []shopRow         `json:"products"`
	Problems   []nicheProblem    `json:"problems"`
	Hints      map[string]string `json:"hints"`
	Counts     map[string]int    `json:"counts"` // товаров с каждой проблемой
	Source     string            `json:"source"` // "" — файл отчета, "ozon" — кабинет по API
	Advice     []shopAdvice      `json:"advice"`
	Economy    *economyView      `json:"economy"`  // nil — у отчета-файла начислений нет
	Losses     *lossesView       `json:"losses"`   // nil — у отчета-файла номеров отправлений нет
	CostsErr   string            `json:"costsErr"` // себестоимость не прочитана: товары есть, прибыли нет
}

// lossesView — возможные пропажи кабинета, крупные сначала; Err — почему не проверено.
type lossesView struct {
	Rows []lossRow `json:"rows"`
	Err  string    `json:"err"`
}

// lossRow — что проверить в кабинете Ozon.
type lossRow struct {
	Kind    string  `json:"kind"`
	Posting string  `json:"posting"`
	SKU     string  `json:"sku"`
	Name    string  `json:"name"`
	Price   float64 `json:"price"`
	Since   string  `json:"since"` // ДД.ММ.ГГГГ
}

// economyView — деньги магазина строками «Экономики магазина» Ozon, в рублях;
// Err — почему начислений нет, когда Ozon в них отказал (товары при этом есть).
type economyView struct {
	Sales      float64 `json:"sales"`
	Commission float64 `json:"commission"`
	Delivery   float64 `json:"delivery"`
	Promotion  float64 `json:"promotion"`
	Partners   float64 `json:"partners"`
	Other      float64 `json:"other"`
	Total      float64 `json:"total"`
	Err        string  `json:"err"`
}

// MarketsShop reads the shop's Ozon product report.
func (a *App) MarketsShop(path string) (shopView, error) { return readShopFile(path) }

// shopAdvice — совет магазину: вид, SKU по важности, штук.
type shopAdvice struct {
	Kind  string   `json:"kind"`
	SKUs  []string `json:"skus"`
	Units float64  `json:"units"`
}

func share4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func shopRowOf(p *domain.ShopProduct) shopRow {
	d := p.Data()
	r := shopRow{Name: d.Name, SKU: d.SKU, Article: d.Article, Scheme: d.Scheme, ABC: d.ABC, PriceIndex: d.PriceIndex, OrderedSum: d.OrderedSum, BoughtSum: d.BoughtOutSum,
		Ordered: d.Ordered, Delivered: d.Delivered, BoughtOut: d.BoughtOut, Cancelled: d.Cancelled, Refused: d.Refused, Returned: d.Returned,
		Buyout: share4(p.BuyoutRate()), Cancel: share4(p.CancelRate()), Refuse: share4(p.RefusedRate()), Return: share4(p.ReturnRate()),
		AvgPrice: d.AvgPrice, DRR: d.DRR, Stock: d.Stock, SupplyQty: d.SupplyQty, Urgent: d.UrgentSupply,
		DaysOut: d.DaysOut, DaysOutOf: d.DaysOutOf, PromoDays: d.PromoDays, Rating: d.Rating, Reviews: d.Reviews, Issues: []string{}}
	for _, i := range p.Issues() {
		r.Issues = append(r.Issues, string(i))
	}
	return r
}

func readShopFile(path string) (shopView, error) {
	rows, err := tablefile.Read(path)
	if err != nil {
		return shopView{}, err
	}
	rep, err := markets.ReadOzonProducts(rows)
	if err != nil {
		return shopView{}, err
	}
	v := shopViewOf(rep)
	v.File, v.Path = filepath.Base(path), path
	return v, nil
}

// shopViewOf turns a report into the window's view, with advice.
func shopViewOf(rep markets.OzonReport) shopView {
	v := shopView{Period: rep.Period, PeriodDays: rep.PeriodDays,
		Products: []shopRow{}, Problems: []nicheProblem{}, Hints: rep.Hints, Counts: map[string]int{}, Advice: adviceOf(rep.Products)}
	if rep.Total != nil {
		v.Total = shopRowOf(rep.Total)
	}
	for _, p := range rep.Products {
		r := shopRowOf(p)
		for _, i := range r.Issues {
			v.Counts[i]++
		}
		v.Products = append(v.Products, r)
	}
	for _, p := range rep.Problems {
		v.Problems = append(v.Problems, nicheProblem{Row: p.Row, Reason: p.Reason})
	}
	return v
}
