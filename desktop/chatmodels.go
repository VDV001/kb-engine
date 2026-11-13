package main

import "github.com/daniil/kb-engine/internal/domain"

// Быстрый выбор модели в поле чата (#584).

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

func modelChoices(current, url string, calls []domain.ModelCall, gw, own map[string]domain.ModelPrice) modelsView {
	return modelsView{}
}

// SetModel saves only the chat model.
func (d *chatDesk) SetModel(model string) error { return nil }
