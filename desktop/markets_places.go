package main

import (
	"context"
	"net/http"
	"time"
)

type placeRow struct {
	Query   string `json:"query"`
	Count   int    `json:"count"`
	Avg     int    `json:"avg"`
	Last    int    `json:"last"`
	LastDay string `json:"lastDay"`
	Seen    int    `json:"seen"`
	Days    int    `json:"days"`
}

type placeStats struct {
	Words       int `json:"words"`
	WordsBefore int `json:"wordsBefore"`
	Avg         int `json:"avg"`
	AvgBefore   int `json:"avgBefore"`
}

type placesView struct {
	SKU    string      `json:"sku"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Total  int         `json:"total"`
	Rows   []placeRow  `json:"rows"`
	Stats  *placeStats `json:"stats"`
	Calls  int         `json:"calls"`
	Source string      `json:"source"`
}

func searchPeriod(now time.Time) (time.Time, time.Time) { return now, now }

func (f *folderSettings) placesCheck(context.Context, *http.Client, string, bool, time.Time) (placesView, error) {
	return placesView{}, nil
}
