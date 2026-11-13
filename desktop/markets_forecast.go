package main

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/tablefile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Маркеты → Прогноз» (#580): три файла — ниша прошлого года, ниша сейчас,
// свои продажи — дают прогноз своих продаж по дням. Файлы окно только читает.

type forecastFile struct {
	File     string `json:"file"`
	Format   string `json:"format"`
	Products int    `json:"products"`
	Days     int    `json:"days"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type forecastDay struct {
	Day   string  `json:"day"` // 2026-11-11
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

type forecastMonth struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

type forecastView struct {
	Files    []forecastFile  `json:"files"`
	Days     []forecastDay   `json:"days"`
	LastYear []forecastDay   `json:"lastYear"`
	Months   []forecastMonth `json:"months"`
	Peak     forecastDay     `json:"peak"`
	Growth   float64         `json:"growth"`
	Share    float64         `json:"share"`
}

var monthNames = [...]string{"", "январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}

// MarketsForecast forecasts own sales from three files.
func (a *App) MarketsForecast(lastYear, nicheNow, own string) (forecastView, error) {
	return forecastFiles(lastYear, nicheNow, own)
}

// round1 — штуки до десятых: хвосты float в окне не нужны.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

func readSales(path, what string) (domain.DailySales, forecastFile, error) {
	if path == "" {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("не выбран файл: %s", what)
	}
	rows, err := tablefile.Read(path)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("%s: %w", what, err)
	}
	s, info, err := markets.ReadDaily(rows)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("%s: %w", what, err)
	}
	return s, forecastFile{File: filepath.Base(path), Format: string(info.Format), Products: info.Products, Days: info.Days,
		From: info.From.Format("02.01.2006"), To: info.To.Format("02.01.2006")}, nil
}

func forecastFiles(lastYear, nicheNow, own string) (forecastView, error) {
	var series [3]domain.DailySales
	v := forecastView{}
	for i, f := range []struct{ path, what string }{{lastYear, "ниша, прошлый год"}, {nicheNow, "ниша, сейчас"}, {own, "свои продажи"}} {
		s, info, err := readSales(f.path, f.what)
		if err != nil {
			return forecastView{}, err
		}
		series[i] = s
		v.Files = append(v.Files, info)
	}
	fc, err := domain.NewForecast(series[0], series[1], series[2])
	if err != nil {
		return forecastView{}, err
	}
	day := func(t time.Time, val, lo, hi float64) forecastDay {
		return forecastDay{Day: t.Format("2006-01-02"), Value: round1(val), Lo: round1(lo), Hi: round1(hi)}
	}
	type ym struct {
		y int
		m time.Month
	}
	var months []ym
	for _, d := range fc.Days() {
		v.Days = append(v.Days, day(d.Day, d.Value, d.Lo, d.Hi))
		if k := (ym{d.Day.Year(), d.Day.Month()}); len(months) == 0 || months[len(months)-1] != k {
			months = append(months, k)
		}
	}
	for _, d := range fc.LastYear() {
		v.LastYear = append(v.LastYear, day(d.Day, d.Value, d.Value, d.Value))
	}
	for _, k := range months {
		val, lo, hi := fc.MonthTotal(k.y, k.m)
		v.Months = append(v.Months, forecastMonth{Label: fmt.Sprintf("%s %d", monthNames[k.m], k.y), Value: math.Round(val), Lo: math.Round(lo), Hi: math.Round(hi)})
	}
	p := fc.Peak()
	v.Peak = day(p.Day, p.Value, p.Lo, p.Hi)
	v.Growth, v.Share = math.Round(fc.Growth()*1e4)/1e4, math.Round(fc.Share()*1e4)/1e4
	if len(v.Days) == 0 {
		return forecastView{}, errors.New("прогноз пустой")
	}
	return v, nil
}
