package main

import (
	"context"
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

type reviewsView struct {
	SKU      string  `json:"sku"`
	Total    int     `json:"total"`
	WithText int     `json:"withText"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Ratings  []int   `json:"ratings"`
	Avg      float64 `json:"avg"`
	Calls    int     `json:"calls"`
	Source   string  `json:"source"`
}

type reviewPointRow struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

type reviewFindingsView struct {
	Praise     []reviewPointRow `json:"praise"`
	Complaints []reviewPointRow `json:"complaints"`
	Used       int              `json:"used"`
	Model      string           `json:"model"`
}

func (f *folderSettings) reviewsCheck(context.Context, *http.Client, string, bool, time.Time) (reviewsView, error) {
	return reviewsView{}, nil
}

func (d *chatDesk) modelReviews([]markets.Review, bool) (reviewFindingsView, error) {
	return reviewFindingsView{}, nil
}
