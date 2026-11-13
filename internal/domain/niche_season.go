package domain

import (
	"errors"
	"time"
)

// ErrUnknownMarketplace is returned for a marketplace the window does not know.
var ErrUnknownMarketplace = errors.New("площадка не узнана")

// NicheMarketplace — колонка площадок ниши: текст, а не число.
const NicheMarketplace NicheColumn = "marketplace"

// Marketplace — площадка продаж.
type Marketplace string

// Площадки.
const (
	MarketWB   Marketplace = "wb"
	MarketOzon Marketplace = "ozon"
)

// MonthSales — продажи ниши за месяц; Year 0 — год в заголовке не указан.
type MonthSales struct {
	Month time.Month
	Year  int
	Value float64
}

// GrowthLevel — как растет спрос ниши.
type GrowthLevel string

// Уровни роста.
const (
	GrowthFast     GrowthLevel = "fast"
	GrowthModerate GrowthLevel = "moderate"
	GrowthFalling  GrowthLevel = "falling"
)

// NicheMissing — чего не хватает для полной оценки.
type NicheMissing string

// Чего может не хватать.
const (
	NicheMissingMonths    NicheMissing = "months"
	NicheMissingGrowth    NicheMissing = "growth"
	NicheMissingTop10     NicheMissing = "top10"
	NicheMissingPerSeller NicheMissing = "perSeller"
)

// NicheScore — оценка ниши и факты, из которых она сложена.
type NicheScore struct {
	Ok           bool
	Score        int
	Missing      []NicheMissing
	HasPerSeller bool
	PerSeller    float64
	Growth       GrowthLevel
	Crowded      bool
	HasPeak      bool
	Peak         time.Month
	InSeason     bool
	WeeksFrom    int
	WeeksTo      int
}

// RecognizeNicheMonth reads a month column header.
func RecognizeNicheMonth(header string) (time.Month, int, bool) { return 0, 0, false }

// ParseMarketplaces reads a marketplaces cell.
func ParseMarketplaces(cell string) ([]Marketplace, error) { return nil, nil }

// WithMonths returns a copy with monthly sales.
func (n *Niche) WithMonths(ms []MonthSales) (*Niche, error) { return n, nil }

// WithMarketplaces returns a copy with marketplaces.
func (n *Niche) WithMarketplaces(ms []Marketplace) (*Niche, error) { return n, nil }

// Months returns monthly sales in file order.
func (n *Niche) Months() []MonthSales { return nil }

// Marketplaces returns where the niche sells.
func (n *Niche) Marketplaces() []Marketplace { return nil }

// PeakMonth returns the month of the highest sales.
func (n *Niche) PeakMonth() (time.Month, bool) { return 0, false }

// ScoreNiches scores niches against each other on now.
func ScoreNiches(ns []*Niche, now time.Time) []NicheScore { return nil }
