package usage_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

var uz = time.FixedZone("UZT", 5*3600)

func mk(t *testing.T, at time.Time, p domain.CallPurpose, model string, in, out int, counted, failed bool) domain.ModelCall {
	t.Helper()
	c, err := domain.NewModelCall(at, p, model, in, out, counted, time.Second, 2*time.Second, failed)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Сводка для экрана «Статистика»: дни периода в часовом поясе человека (и
// пустые тоже — график без дыр), назначения и модели, итоги периода и
// прошлого такого же периода; вызовы без счета считаются отдельно, а не
// нулями в токенах.
func TestReport(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, uz)
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, uz) }
	calls := []domain.ModelCall{
		mk(t, day(7, 10), domain.CallAnswer, "sonnet", 5000, 600, true, false),
		mk(t, day(7, 10), domain.CallSearch, "sonnet", 3000, 200, true, false),
		mk(t, day(6, 23), domain.CallExtract, "sonnet", 900, 60, true, false),
		// 00:30 по Ташкенту — это еще 6-е в UTC: день — по поясу человека
		mk(t, time.Date(2026, 11, 6, 19, 30, 0, 0, time.UTC), domain.CallAnswer, "qwen3", 0, 0, false, true),
		mk(t, day(4, 12), domain.CallAnswer, "sonnet", 4000, 500, true, false), // прошлый период
		mk(t, day(1, 12), domain.CallAnswer, "sonnet", 1, 1, true, false),      // раньше обоих
	}
	r := usage.NewReport(calls, now, 3)
	if len(r.Days) != 3 || r.Days[0].Date != "2026-11-05" || r.Days[2].Date != "2026-11-07" {
		t.Fatalf("дни = %+v", r.Days)
	}
	if d := r.Days[2]; d.In != 8000 || d.Out != 800 || d.Answers != 2 || d.Searches != 1 || d.Calls != 3 || d.Failed != 1 || d.Uncounted != 1 {
		t.Fatalf("7 ноября = %+v", d)
	}
	if d := r.Days[1]; d.Calls != 1 || d.In != 900 {
		t.Fatalf("6 ноября = %+v", d)
	}
	if d := r.Days[0]; d.Calls != 0 {
		t.Fatalf("пустой день = %+v", d)
	}
	if r.Total.Calls != 4 || r.Total.In != 8900 || r.Total.Uncounted != 1 || r.Prev.Calls != 1 || r.Prev.In != 4000 {
		t.Fatalf("итоги %+v, прошлый %+v", r.Total, r.Prev)
	}
	want := map[domain.CallPurpose]int{domain.CallAnswer: 2, domain.CallSearch: 1, domain.CallExtract: 1}
	for _, p := range r.Purposes {
		if p.Calls != want[p.Purpose] {
			t.Errorf("%s: %d вызовов", p.Purpose, p.Calls)
		}
	}
	if len(r.Models) != 2 || r.Models[0].Model != "sonnet" || r.Models[0].Calls != 3 {
		t.Fatalf("модели = %+v", r.Models)
	}
	if len(r.Calls) != 4 || !r.Calls[0].At().Equal(day(7, 10)) {
		t.Fatalf("вызовы периода, свежие первыми: %d", len(r.Calls))
	}
}
