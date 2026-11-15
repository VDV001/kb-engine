package markets_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Задание на инфографику (#609) — из карточки: название, пол, цвет, описание
// и главный запрос ниши; длинное описание обрезается, пустые поля не пишутся.
func TestInfographicPrompt(t *testing.T) {
	in := markets.DescriptionInput{Title: "Футболка женская оверсайз базовая", Gender: "Женский", Color: "сиреневый",
		Description: "Хлопок 100 %. " + strings.Repeat("Мягкая ткань. ", 300),
		Queries:     []markets.NicheQuery{{Text: "футболка женская", Count: 111685, Items: 778}}}
	p := markets.InfographicPrompt(in)
	for _, want := range []string{"Футболка женская оверсайз базовая", "Пол: Женский", "Цвет: сиреневый", "Хлопок 100 %", "футболка женская"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt has no %q:\n%s", want, p)
		}
	}
	if n := len([]rune(p)); n > 2500 {
		t.Errorf("prompt is %d runes; a long description must be clipped", n)
	}
	if p := markets.InfographicPrompt(markets.DescriptionInput{Title: "Брюки"}); strings.Contains(p, "Пол:") || strings.Contains(p, "Цвет:") || strings.Contains(p, "запрос") {
		t.Errorf("empty fields written:\n%s", p)
	}
	for _, rule := range []string{"3:4", "не выдумывай", "цен", "референс"} {
		if !strings.Contains(markets.InfographicSystem, rule) {
			t.Errorf("system has no rule %q", rule)
		}
	}
}
