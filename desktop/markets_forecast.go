package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/usage"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
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
	Undated  int    `json:"undated"` // отправлений кабинета без даты: не в ряду
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

// MarketsForecast forecasts own sales: две ниши файлами, свои — файлом или
// «ozon:<id>» из кабинета.
func (a *App) MarketsForecast(lastYear, nicheNow, own string) (forecastView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.forecast(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, own, time.Now())
}

// round1 — штуки до десятых: хвосты float в окне не нужны.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

func readSales(path, what string) (domain.DailySales, forecastFile, error) {
	if path == "" {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("не выбран файл: %s", what)
	}
	rows, err := tablefile.Read(path)
	if errors.Is(err, tablefile.ErrTooBig) && itemsCSV(path) {
		err = markets.ErrItemsNotDaily
	}
	if err != nil {
		return domain.DailySales{}, forecastFile{}, salesErr(what, err)
	}
	s, info, err := markets.ReadDaily(rows)
	if errors.Is(err, markets.ErrNoDaily) && len(rows) > 0 && markets.IsItems(rows[0]) {
		err = markets.ErrItemsNotDaily
	}
	if err != nil {
		return domain.DailySales{}, forecastFile{}, salesErr(what, err)
	}
	return s, forecastFile{File: filepath.Base(path), Format: string(info.Format), Products: info.Products, Days: info.Days,
		From: info.From.Format("02.01.2006"), To: info.To.Format("02.01.2006")}, nil
}

// itemsCSV reports whether a csv is MPStats «Товары категории» by its first
// row: та выгрузка весит сотни МБ, и предел Read назвал бы размер, а не
// настоящую причину (живая проверка 13.11). Только csv: xlsx потоком не читается.
func itemsCSV(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".csv") {
		return false
	}
	for row, err := range tablefile.Rows(path) {
		return err == nil && markets.IsItems(row)
	}
	return false
}

// salesErr names the file; выгрузке товаров — что принести вместо нее.
func salesErr(what string, err error) error {
	if errors.Is(err, markets.ErrItemsNotDaily) {
		return fmt.Errorf("%s: %w — нужна выгрузка продаж ниши по дням", what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// readNiche reads the two niche files: прошлый год и сейчас.
func readNiche(lastYear, nicheNow string) ([2]domain.DailySales, []forecastFile, error) {
	var series [2]domain.DailySales
	var files []forecastFile
	for i, f := range []struct{ path, what string }{{lastYear, "ниша, прошлый год"}, {nicheNow, "ниша, сейчас"}} {
		s, info, err := readSales(f.path, f.what)
		if err != nil {
			return series, nil, err
		}
		series[i], files = s, append(files, info)
	}
	return series, files, nil
}

// forecastOf builds the view from the three series: ниша прошлого года, ниша сейчас, свои.
func forecastOf(series [3]domain.DailySales, files []forecastFile) (forecastView, error) {
	v := forecastView{Files: files}
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

type modelMonth struct {
	Label   string  `json:"label"`
	Model   float64 `json:"model"`
	Formula float64 `json:"formula"`
	Diff    float64 `json:"diff"` // модель к формуле: 0.1 — на 10 % больше
}

type modelForecastView struct {
	Model  string       `json:"model"`
	Months []modelMonth `json:"months"`
	Peak   string       `json:"peak"`
	Note   string       `json:"note"`
}

// ModelForecast asks the chat model for the forecast too (решение владельца
// 13.11): рядом с формулой и с пометкой «перепроверьте». consent — человек
// увидел, что суммы по неделям уйдут провайдеру модели; без него вызова нет.
func (d *chatDesk) ModelForecast(lastYear, nicheNow, own string, consent bool) (modelForecastView, error) {
	if !consent {
		return modelForecastView{}, errors.New("суммы продаж по неделям уйдут провайдеру модели: подтвердите в окне")
	}
	niche, _, err := readNiche(lastYear, nicheNow)
	if err != nil {
		return modelForecastView{}, err
	}
	s, _, err := readSales(own, "свои продажи")
	if err != nil {
		return modelForecastView{}, err
	}
	return d.modelForecast([3]domain.DailySales{niche[0], niche[1], s})
}

// modelReady checks the chat model's key before the window goes to Ozon for data.
func (d *chatDesk) modelReady() error {
	_, err := d.modelKey(d.config())
	return err
}

// modelForecast asks the model about the three series; согласие проверено выше.
func (d *chatDesk) modelForecast(series [3]domain.DailySales) (modelForecastView, error) {
	formula, err := domain.NewForecast(series[0], series[1], series[2])
	if err != nil {
		return modelForecastView{}, err
	}
	c := d.config()
	key, err := d.modelKey(c)
	if err != nil {
		return modelForecastView{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	model := d.metered(d.model(c, key), c, usage.Fixed(domain.CallForecast), "")
	reply, err := model.Complete(ctx, []chat.ModelMessage{
		{Role: chat.RoleSystem, Content: markets.ForecastSystem},
		{Role: chat.RoleUser, Content: markets.ForecastPrompt(series[0], series[1], series[2])},
	}, nil)
	if err != nil {
		return modelForecastView{}, errors.New(chatError(err))
	}
	mf, err := markets.ParseModelForecast(reply.Content)
	if err != nil {
		return modelForecastView{}, err
	}
	v := modelForecastView{Model: c.Model, Peak: mf.Peak.Format("2006-01-02"), Note: mf.Note}
	for _, m := range mf.Months {
		f, _, _ := formula.MonthTotal(m.Year, m.Month)
		mm := modelMonth{Label: fmt.Sprintf("%s %d", monthNames[m.Month], m.Year), Model: math.Round(m.Value), Formula: math.Round(f)}
		if f > 0 {
			mm.Diff = math.Round((m.Value/f-1)*1000) / 1000
		}
		v.Months = append(v.Months, mm)
	}
	return v, nil
}

// MarketsModelForecast asks the chat model for the forecast next to the formula.
// Свои продажи «ozon:<id>» — из кабинета, ниша «mpstats:…» — из MPStats;
// согласие спрашивается до похода в сеть.
func (a *App) MarketsModelForecast(lastYear, nicheNow, own string, consent bool) (modelForecastView, error) {
	// Кабинет Ozon или ниша из MPStats — сначала ключ модели, потом сеть (ревью #592).
	net := strings.HasPrefix(own, "ozon:") || strings.HasPrefix(lastYear, "mpstats:") || strings.HasPrefix(nicheNow, "mpstats:")
	if !net || !consent {
		return a.chat.ModelForecast(lastYear, nicheNow, own, consent)
	}
	if err := a.chat.modelReady(); err != nil { // без ключа модели в Ozon не ходим (ревью #592)
		return modelForecastView{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	series, _, err := a.settings.forecastInputs(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, own, time.Now())
	if err != nil {
		return modelForecastView{}, err
	}
	return a.chat.modelForecast(series)
}
