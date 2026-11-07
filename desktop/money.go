package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/modelprices"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// pricesURL — список моделей шлюза, откуда «Деньги» берут цены (решение
// владельца 07.11: цены шлюза, своя правка в настройках).
// ponytail: цены всегда из Vercel AI Gateway, даже если чат идет к другому
// провайдеру; цена прямого провайдера может отличаться — тогда своя правка.
// Путь выше — брать /models у адреса чата, когда тот отдает pricing.
const pricesURL = "https://ai-gateway.vercel.sh/v1"

// pricesTimeout — сколько ждать шлюз: «Деньги» не висят на сети.
const pricesTimeout = 10 * time.Second

// priceList gives model prices by id; ошибка — почему цены не свежие (могут быть и вчерашние).
type priceList func(context.Context) (map[string]domain.ModelPrice, error)

func gatewayPriceList(dir string) priceList {
	c := &modelprices.Cache{Path: filepath.Join(filepath.Dir(dir), "prices.json"), BaseURL: pricesURL, Now: time.Now}
	return c.Load
}

// ownFirst is the price book: своя цена главнее шлюза.
type ownFirst struct{ own, gateway map[string]domain.ModelPrice }

func (b ownFirst) Price(model string) (domain.ModelPrice, bool) {
	if p, ok := b.own[model]; ok {
		return p, true
	}
	p, ok := b.gateway[model]
	return p, ok
}

type moneyGroup struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Cost  float64 `json:"cost"` // долларов
	Calls int     `json:"calls"`
}

type moneyModel struct {
	ID     string  `json:"id"`
	Cost   float64 `json:"cost"`
	Calls  int     `json:"calls"`
	Priced bool    `json:"priced"`
	// Priceable — у модели есть вызовы со счетом; у облачной речи (всегда без
	// счета) своя цена ничего бы не поменяла — окно не дает ее вводить.
	Priceable bool   `json:"priceable"`
	Source    string `json:"source"` // «своя», «шлюз» или пусто — цены нет
	In        string `json:"in"`     // долларов за 1 млн токенов
	Out       string `json:"out"`
}

// moneyView is the month's spend for the page; доллары — только для показа,
// считается все в пикодолларах.
type moneyView struct {
	Month       string       `json:"month"`
	DaysPassed  int          `json:"daysPassed"`
	DaysInMonth int          `json:"daysInMonth"`
	Spent       float64      `json:"spent"`
	Forecast    float64      `json:"forecast"`
	Days        []float64    `json:"days"`
	Purposes    []moneyGroup `json:"purposes"`
	Models      []moneyModel `json:"models"`
	Uncounted   int          `json:"uncounted"`
	Unpriced    int          `json:"unpriced"`
	Limit       string       `json:"limit"`
	PriceNote   string       `json:"priceNote"` // почему цены шлюза не свежие
}

const picoPerDollar = 1e12

func dollars(pico int64) float64 { return float64(pico) / picoPerDollar }

// StatsMoney returns the spend of the current month.
func (d *chatDesk) StatsMoney() (moneyView, error) {
	if d.calls == nil {
		return moneyView{}, errors.New("журнал вызовов модели не ведется")
	}
	calls, _, err := d.calls.Load()
	if err != nil {
		return moneyView{}, err
	}
	d.settings.mu.Lock()
	spend := d.settings.cfg.Spend
	d.settings.mu.Unlock()
	own := make(map[string]domain.ModelPrice, len(spend.Prices))
	for m, p := range spend.Prices {
		if mp, err := domain.ParseModelPricePerMillion(p.In, p.Out); err == nil {
			own[m] = mp
		}
	}
	v := moneyView{Limit: spend.Limit, Purposes: []moneyGroup{}, Models: []moneyModel{}}
	gw := map[string]domain.ModelPrice{}
	if d.prices != nil {
		ctx, cancel := context.WithTimeout(context.Background(), pricesTimeout)
		gw, err = d.prices(ctx)
		cancel()
		if err != nil {
			v.PriceNote = err.Error()
		}
	}
	book := ownFirst{own, gw}
	m := usage.NewMoney(calls, d.now(), book)
	v.Month, v.DaysPassed, v.DaysInMonth = m.Month, m.DaysPassed, m.DaysInMonth
	v.Spent, v.Forecast, v.Uncounted, v.Unpriced = dollars(m.Spent), dollars(m.Forecast), m.Uncounted, m.Unpriced
	for _, x := range m.Days {
		v.Days = append(v.Days, dollars(x))
	}
	for _, p := range m.Purposes {
		v.Purposes = append(v.Purposes, moneyGroup{string(p.Purpose), purposeTitles[p.Purpose], dollars(p.Cost), p.Calls})
	}
	for _, x := range m.Models {
		mm := moneyModel{ID: x.Model, Cost: dollars(x.Cost), Calls: x.Calls, Priced: x.Priced, Priceable: x.Counted > 0}
		if p, ok := book.Price(x.Model); ok {
			mm.In, mm.Out = p.PerMillion()
			mm.Source = "шлюз"
			if _, mine := own[x.Model]; mine {
				mm.Source = "своя"
			}
		}
		v.Models = append(v.Models, mm)
	}
	return v, nil
}

// SetModelPrice sets a model's own price per million tokens; пустые вход и
// выход снимают свою цену — снова цена шлюза.
func (d *chatDesk) SetModelPrice(model, in, out string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("%w: модель не указана", desktopconfig.ErrSpend)
	}
	in, out = strings.TrimSpace(in), strings.TrimSpace(out)
	if in != "" || out != "" {
		if _, err := domain.ParseModelPricePerMillion(in, out); err != nil {
			return err
		}
	}
	return d.settings.updateConfig(func(c *desktopconfig.Config) {
		c.Spend.Prices = maps.Clone(c.Spend.Prices) // карта общая с текущим конфигом: правка — на копии
		if in == "" && out == "" {
			delete(c.Spend.Prices, model)
			return
		}
		if c.Spend.Prices == nil {
			c.Spend.Prices = map[string]desktopconfig.Price{}
		}
		c.Spend.Prices[model] = desktopconfig.Price{In: in, Out: out}
	})
}

// SetSpendLimit sets the monthly limit in dollars; пусто — без лимита.
func (d *chatDesk) SetSpendLimit(usd string) error {
	usd = strings.TrimSpace(usd)
	if err := desktopconfig.CheckLimit(usd); err != nil {
		return err
	}
	return d.settings.updateConfig(func(c *desktopconfig.Config) { c.Spend.Limit = usd })
}
