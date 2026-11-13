package main

import (
	"strings"
	"testing"
	"time"
)

func TestModelForecast(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	last := longCSV(t, "прошлый.csv", at(2025, 9, 1), at(2025, 12, 31), func(d time.Time) int {
		if d.Month() >= time.November {
			return 200
		}
		return 100
	})
	now := longCSV(t, "сейчас.csv", at(2026, 9, 1), at(2026, 10, 31), func(time.Time) int { return 150 })
	own := longCSV(t, "свои.csv", at(2026, 9, 1), at(2026, 10, 31), func(time.Time) int { return 15 })

	cd, m := localChat(t)
	if _, err := cd.ModelForecast(last, now, own, false); err == nil || m.n != 0 {
		t.Fatalf("без согласия: ошибка %v, вызовов модели %d", err, m.n)
	}
	m.reply = "```json\n{\"months\":[{\"month\":\"2026-11\",\"value\":990},{\"month\":\"2026-12\",\"value\":930}],\"peak\":\"2026-12-20\",\"note\":\"по форме прошлого года\"}\n```"
	v, err := cd.ModelForecast(last, now, own, true)
	if err != nil || m.n != 1 {
		t.Fatalf("ModelForecast: %v, вызовов %d", err, m.n)
	}
	// Формула: 30 шт в день — ноябрь 900, декабрь 930; модель — 990 и 930.
	if len(v.Months) != 2 || v.Months[0].Label != "ноябрь 2026" || v.Months[0].Model != 990 || v.Months[0].Formula != 900 || v.Months[0].Diff != 0.1 {
		t.Errorf("months = %+v, want ноябрь: модель 990, формула 900, +10 %%", v.Months)
	}
	if v.Peak != "2026-12-20" || v.Note != "по форме прошлого года" || v.Model == "" {
		t.Errorf("peak = %q, note = %q, model = %q", v.Peak, v.Note, v.Model)
	}
	var prompt strings.Builder
	for _, msg := range m.asked[0] {
		prompt.WriteString(msg.Content + "\n")
	}
	if strings.Contains(prompt.String(), "прошлый.csv") || !strings.Contains(prompt.String(), "неделя с 01.09.2025") {
		t.Errorf("в запросе должны быть суммы по неделям и не должно быть имен файлов:\n%s", prompt.String())
	}

	m.reply = "не знаю"
	if _, err := cd.ModelForecast(last, now, own, true); err == nil || !strings.Contains(err.Error(), "модель не дала прогноз") {
		t.Errorf("bad answer: err = %v", err)
	}
}
