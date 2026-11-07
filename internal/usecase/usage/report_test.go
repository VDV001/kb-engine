package usage_test

import (
	"slices"
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
	if d := r.Days[2]; d.In != 8000 || d.Out != 800 || d.Calls != 3 || d.Failed != 1 || d.Uncounted != 1 {
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

// Токены — только по вызовам со счетом: строка «без счета» с числами (правка
// журнала руками, сервер прислал мусор) не попадает в суммы ни дня, ни итога,
// ни назначения, ни модели.
func TestReport_uncountedTokensAreNotSummed(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, uz)
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, uz)
	calls := []domain.ModelCall{
		mk(t, at, domain.CallAnswer, "sonnet", 100, 10, true, false),
		mk(t, at, domain.CallAnswer, "sonnet", 7000, 700, false, false),
		mk(t, now.AddDate(0, 0, -1), domain.CallAnswer, "sonnet", 9000, 900, false, false), // прошлый период при days=1
	}
	r := usage.NewReport(calls, now, 1)
	for name, x := range map[string]usage.Totals{"день": r.Days[0].Totals, "итог": r.Total, "назначение": r.Purposes[0].Totals, "модель": r.Models[0].Totals} {
		if x.In != 100 || x.Out != 10 || x.Calls != 2 || x.Uncounted != 1 {
			t.Errorf("%s = %+v", name, x)
		}
	}
	if r.Prev.In != 0 || r.Prev.Uncounted != 1 {
		t.Errorf("прошлый = %+v", r.Prev)
	}
}

// Назначения с равными токенами идут по имени, а не в порядке обхода map:
// иначе кольцо и полоски меняют порядок от открытия к открытию.
func TestReport_purposeOrderIsStable(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, uz)
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, uz)
	calls := []domain.ModelCall{
		mk(t, at, domain.CallSearch, "m", 10, 0, true, false),
		mk(t, at, domain.CallAnswer, "m", 10, 0, true, false),
		mk(t, at, domain.CallExtract, "m", 10, 0, true, false),
		mk(t, at, domain.CallCompress, "m", 50, 0, true, false),
	}
	want := []domain.CallPurpose{domain.CallCompress, domain.CallAnswer, domain.CallExtract, domain.CallSearch}
	for range 50 {
		r := usage.NewReport(calls, now, 1)
		var got []domain.CallPurpose
		for _, p := range r.Purposes {
			got = append(got, p.Purpose)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("порядок = %v, нужен %v", got, want)
		}
	}
}
