package markets_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func flat(t *testing.T, from, to time.Time, v float64) domain.DailySales {
	t.Helper()
	var ds []domain.DaySales
	for x := from; !x.After(to); x = x.AddDate(0, 0, 1) {
		ds = append(ds, domain.DaySales{Day: x, Value: v})
	}
	s, err := domain.NewDailySales(ds)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestForecastPrompt(t *testing.T) {
	last := flat(t, d(2025, 9, 1), d(2025, 12, 31), 100)
	now := flat(t, d(2026, 9, 1), d(2026, 10, 31), 150)
	own := flat(t, d(2026, 9, 1), d(2026, 10, 31), 15)
	p := markets.ForecastPrompt(last, now, own)
	for _, want := range []string{
		"01.09.2025–31.12.2025", "01.09.2026–31.10.2026", // периоды рядов
		"неделя с 01.09.2025: 700", "неделя с 01.09.2026: 1050", "неделя с 01.09.2026: 105", // суммы по неделям
		`"months"`, `"peak"`, "JSON", // формат ответа
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if n := strings.Count(p, "\n"); n > 80 {
		t.Errorf("prompt has %d lines: по неделям, а не по дням — модели не нужен каждый день", n)
	}
}

func TestParseModelForecast(t *testing.T) {
	got, err := markets.ParseModelForecast("Вот прогноз:\n```json\n{\"months\":[{\"month\":\"2026-11\",\"value\":950},{\"month\":\"2026-12\",\"value\":1000.4}],\"peak\":\"2026-11-11\",\"note\":\"всплеск 11.11\"}\n```")
	if err != nil {
		t.Fatalf("ParseModelForecast: %v", err)
	}
	if len(got.Months) != 2 || got.Months[0].Year != 2026 || got.Months[0].Month != time.November || got.Months[1].Value != 1000.4 {
		t.Errorf("months = %+v", got.Months)
	}
	if !got.Peak.Equal(d(2026, 11, 11)) || got.Note != "всплеск 11.11" {
		t.Errorf("peak = %v, note = %q", got.Peak, got.Note)
	}
	for name, text := range map[string]string{
		"нет JSON":       "Не могу посчитать без данных",
		"пустые месяцы":  `{"months":[],"peak":"2026-11-11"}`,
		"меньше нуля":    `{"months":[{"month":"2026-11","value":-5}],"peak":"2026-11-11"}`,
		"месяц не месяц": `{"months":[{"month":"ноябрь","value":5}],"peak":"2026-11-11"}`,
		"пик не дата":    `{"months":[{"month":"2026-11","value":5}],"peak":"скоро"}`,
	} {
		if _, err := markets.ParseModelForecast(text); !errors.Is(err, markets.ErrModelForecast) {
			t.Errorf("%s: err = %v, want ErrModelForecast", name, err)
		}
	}
}
