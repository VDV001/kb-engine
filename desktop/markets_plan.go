package main

import (
	"context"
	"net/http"
	"time"
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

func (f *folderSettings) plan(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput) (planView, error) {
	return planView{}, nil
}

func (f *folderSettings) planExcel(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time, in planInput, path string) error {
	return nil
}
