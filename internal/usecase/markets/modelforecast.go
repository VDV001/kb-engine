package markets

import (
	"errors"
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

// ModelForecast — прогноз модели: по месяцам, пик и пояснение.
type ModelForecast struct {
	Months []MonthValue
	Peak   time.Time
	Note   string
}

// ForecastPrompt builds the model's task from the three series.
func ForecastPrompt(lastYear, nicheNow, own domain.DailySales) string { return "" }

// ParseModelForecast reads the model's JSON answer.
func ParseModelForecast(text string) (ModelForecast, error) { return ModelForecast{}, nil }
