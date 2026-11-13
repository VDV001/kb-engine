package markets

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrModelForecast is returned when the model's answer is not a forecast.
var ErrModelForecast = errors.New("модель не дала прогноз")

// MonthValue — прогноз модели на месяц.
type MonthValue struct {
	Year  int
	Month time.Month
	Value float64
}

// ModelForecast — прогноз модели: по месяцам, пик и пояснение. Решение
// владельца 13.11: модель считает тоже, рядом с формулой, с пометкой
// «перепроверьте» — ее числа тестом не проверить.
type ModelForecast struct {
	Months []MonthValue
	Peak   time.Time
	Note   string
}

// ForecastSystem is the model's role for the forecast.
const ForecastSystem = "Ты аналитик продаж на маркетплейсах. Считай по данным, не выдумывай. Отвечай только JSON без пояснений вокруг."

// ForecastPrompt builds the model's task from the three series: только суммы
// по неделям и периоды — ни названий товаров, ни продавцов.
func ForecastPrompt(lastYear, nicheNow, own domain.DailySales) string {
	var b strings.Builder
	b.WriteString("Спрогнозируй продажи продавца в штуках по месяцам для периода, который в прошлом году шел после периода «сейчас», и день пика продаж.\n")
	b.WriteString("Учитывай форму сезона прошлого года, рост ниши к прошлому году и долю продавца в нише.\n\n")
	for _, s := range []struct {
		name string
		ds   domain.DailySales
	}{{"Ниша, прошлый год", lastYear}, {"Ниша, сейчас", nicheNow}, {"Продавец, сейчас", own}} {
		days := s.ds.Days()
		if len(days) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s, %s–%s, штук по неделям:\n", s.name, days[0].Day.Format("02.01.2006"), days[len(days)-1].Day.Format("02.01.2006"))
		for i := 0; i < len(days); i += 7 {
			sum := 0.0
			for _, x := range days[i:min(i+7, len(days))] {
				sum += x.Value
			}
			fmt.Fprintf(&b, "неделя с %s: %s\n", days[i].Day.Format("02.01.2006"), formatQty(sum))
		}
		b.WriteString("\n")
	}
	b.WriteString(`Ответ — JSON: {"months":[{"month":"YYYY-MM","value":число штук}],"peak":"YYYY-MM-DD","note":"одна фраза, на что опирался"}`)
	return b.String()
}

func formatQty(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// ParseModelForecast reads the model's JSON answer; вокруг JSON может быть
// текст или ```json — берется первый объект целиком.
func ParseModelForecast(text string) (ModelForecast, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ModelForecast{}, fmt.Errorf("%w: в ответе нет JSON", ErrModelForecast)
	}
	var raw struct {
		Months []struct {
			Month string  `json:"month"`
			Value float64 `json:"value"`
		} `json:"months"`
		Peak string `json:"peak"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return ModelForecast{}, fmt.Errorf("%w: %w", ErrModelForecast, err)
	}
	if len(raw.Months) == 0 {
		return ModelForecast{}, fmt.Errorf("%w: нет месяцев", ErrModelForecast)
	}
	out := ModelForecast{Note: strings.TrimSpace(raw.Note)}
	for _, m := range raw.Months {
		t, err := time.Parse("2006-01", strings.TrimSpace(m.Month))
		if err != nil {
			return ModelForecast{}, fmt.Errorf("%w: месяц %q", ErrModelForecast, m.Month)
		}
		if m.Value < 0 || math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			return ModelForecast{}, fmt.Errorf("%w: %s меньше нуля", ErrModelForecast, m.Month)
		}
		out.Months = append(out.Months, MonthValue{Year: t.Year(), Month: t.Month(), Value: m.Value})
	}
	p, err := time.Parse("2006-01-02", strings.TrimSpace(raw.Peak))
	if err != nil {
		return ModelForecast{}, fmt.Errorf("%w: пик %q", ErrModelForecast, raw.Peak)
	}
	out.Peak = p
	return out, nil
}
