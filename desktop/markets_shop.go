package main

// «Маркеты → Магазин» (#580): отчет Ozon «По товарам» своего магазина.

type shopRow struct {
	Name      string   `json:"name"`
	SKU       string   `json:"sku"`
	Ordered   float64  `json:"ordered"`
	Buyout    float64  `json:"buyout"`
	Cancel    float64  `json:"cancel"`
	Urgent    bool     `json:"urgent"`
	DaysOut   int      `json:"daysOut"`
	DaysOutOf int      `json:"daysOutOf"`
	Issues    []string `json:"issues"`
}

type shopView struct {
	File       string            `json:"file"`
	Period     string            `json:"period"`
	PeriodDays int               `json:"periodDays"`
	Total      shopRow           `json:"total"`
	Products   []shopRow         `json:"products"`
	Problems   []nicheProblem    `json:"problems"`
	Hints      map[string]string `json:"hints"`
	Counts     map[string]int    `json:"counts"`
}

func readShopFile(path string) (shopView, error) { return shopView{}, nil }
