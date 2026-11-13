package main

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Быстрый выбор модели в поле чата (#584): чип под полем открывает список —
// последние модели из журнала вызовов и все модели шлюза с ценой; кнопка
// «Настройки чата» слева ведет в настройки (адрес, ключ, кто ищет).

// recentModels — сколько последних моделей показывать сверху.
const recentModels = 3

// modelChoice — модель в списке и ее цена за миллион токенов, $; пусто — цена неизвестна.
type modelChoice struct {
	ID  string `json:"id"`
	In  string `json:"in"`
	Out string `json:"out"`
	Own bool   `json:"own"` // цена своя, из «Статистики»
}

// modelsView — что показывает список: текущая, последние и все.
type modelsView struct {
	Current   string        `json:"current"`
	Gateway   bool          `json:"gateway"` // адрес чата — шлюз Vercel: в списке все его модели
	Recent    []modelChoice `json:"recent"`
	All       []modelChoice `json:"all"`
	PriceNote string        `json:"priceNote"`
}

// isGateway reports whether the chat address is the Vercel AI Gateway, чьи
// модели и цены знает окно; у другого адреса (Ollama, свой сервер) — свои имена.
func isGateway(chatURL string) bool {
	u, err := url.Parse(strings.TrimSpace(chatURL))
	g, _ := url.Parse(pricesURL)
	return err == nil && u.Host == g.Host
}

func modelChoices(current, chatURL string, calls []domain.ModelCall, gw, own map[string]domain.ModelPrice) modelsView {
	v := modelsView{Current: current, Gateway: isGateway(chatURL), Recent: []modelChoice{}, All: []modelChoice{}}
	choice := func(id string) modelChoice {
		if p, ok := own[id]; ok {
			in, out := p.PerMillion()
			return modelChoice{ID: id, In: in, Out: out, Own: true}
		}
		if p, ok := gw[id]; ok && v.Gateway {
			in, out := p.PerMillion()
			return modelChoice{ID: id, In: in, Out: out}
		}
		return modelChoice{ID: id}
	}
	ids := map[string]bool{}
	for i, m := range usedModels(calls) {
		ids[m] = true
		if i < recentModels {
			v.Recent = append(v.Recent, choice(m))
		}
	}
	if current != "" {
		ids[current] = true
	}
	for id := range own {
		ids[id] = true
	}
	if v.Gateway {
		for id := range gw {
			ids[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		v.All = append(v.All, choice(id))
	}
	return v
}

// usedModels lists the models of the call journal, последняя по времени первой, без повторов.
func usedModels(calls []domain.ModelCall) []string {
	byTime := slices.Clone(calls)
	slices.SortStableFunc(byTime, func(a, b domain.ModelCall) int { return b.At().Compare(a.At()) })
	var out []string
	for _, c := range byTime {
		if m := c.Model(); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// Models returns the quick picker list; цены шлюза — из кэша, без них список остается.
func (d *chatDesk) Models() (modelsView, error) {
	d.settings.mu.Lock()
	c, spend := d.settings.cfg.Chat.WithDefaults(), d.settings.cfg.Spend
	d.settings.mu.Unlock()
	var calls []domain.ModelCall
	if d.calls != nil {
		var err error
		if calls, _, err = d.calls.Load(); err != nil {
			return modelsView{}, err
		}
	}
	own := make(map[string]domain.ModelPrice, len(spend.Prices))
	for m, p := range spend.Prices {
		if mp, err := domain.ParseModelPricePerMillion(p.In, p.Out); err == nil {
			own[m] = mp
		}
	}
	gw := map[string]domain.ModelPrice{}
	note := ""
	if d.prices != nil && isGateway(c.URL) {
		ctx, cancel := context.WithTimeout(context.Background(), pricesTimeout)
		var err error
		if gw, err = d.prices(ctx); err != nil {
			note = err.Error()
		}
		cancel()
	}
	v := modelChoices(c.Model, c.URL, calls, gw, own)
	v.PriceNote = note
	return v, nil
}

// SetModel saves only the chat model; адрес, ключ и остальное остаются.
func (d *chatDesk) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("модель не выбрана")
	}
	d.settings.mu.Lock()
	c := d.settings.cfg.Chat
	d.settings.mu.Unlock()
	c.Model = model
	return d.SetSettings(c, "")
}
