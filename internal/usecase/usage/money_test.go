package usage_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

type priceBook map[string]domain.ModelPrice

func (b priceBook) Price(model string) (domain.ModelPrice, bool) { p, ok := b[model]; return p, ok }

func mustPrice(t *testing.T, in, out string) domain.ModelPrice {
	t.Helper()
	p, err := domain.ParseModelPricePerMillion(in, out)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func call(t *testing.T, at time.Time, p domain.CallPurpose, model string, in, out int, counted bool) domain.ModelCall {
	t.Helper()
	c, err := domain.NewModelCall(at, p, model, in, out, counted, time.Second, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// «Деньги» — текущий месяц в поясе человека: расход по вызовам со счетом и
// ценой; без счета и без цены — отдельными числами, а не нулем; прогноз до
// конца месяца — расход в среднем за прошедшие дни, растянутый на месяц.
func TestMoney(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	book := priceBook{"haiku": mustPrice(t, "1", "5"), "sonnet": mustPrice(t, "2", "10")}
	calls := []domain.ModelCall{
		call(t, time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), domain.CallAnswer, "sonnet", 1_000_000, 0, true), // прошлый месяц
		call(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), domain.CallAnswer, "haiku", 1_000_000, 100_000, true),
		call(t, time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC), domain.CallSearch, "sonnet", 500_000, 100_000, true),
		call(t, time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC), domain.CallAnswer, "sonnet", 0, 0, false),      // без счета
		call(t, time.Date(2026, 11, 7, 11, 30, 0, 0, time.UTC), domain.CallExtract, "local", 9000, 900, true), // без цены
	}
	m := usage.NewMoney(calls, now, book)
	const usd = int64(1_000_000_000_000)
	// haiku: 1 млн × $1 + 0,1 млн × $5 = $1,5; sonnet: 0,5 × $2 + 0,1 × $10 = $2
	if m.Month != "2026-11" || m.DaysPassed != 7 || m.DaysInMonth != 30 {
		t.Fatalf("месяц %q, дней %d из %d", m.Month, m.DaysPassed, m.DaysInMonth)
	}
	if m.Spent != 35*usd/10 {
		t.Fatalf("Spent = %d", m.Spent)
	}
	if m.Forecast != 35*usd/10*30/7 {
		t.Fatalf("Forecast = %d", m.Forecast)
	}
	if m.Uncounted != 1 || m.Unpriced != 1 {
		t.Fatalf("без счета %d, без цены %d", m.Uncounted, m.Unpriced)
	}
	if len(m.Days) != 7 || m.Days[0] != 15*usd/10 || m.Days[6] != 2*usd {
		t.Fatalf("по дням %v", m.Days)
	}
	if len(m.Purposes) != 3 || m.Purposes[0].Purpose != domain.CallSearch || m.Purposes[0].Cost != 2*usd || m.Purposes[1].Purpose != domain.CallAnswer {
		t.Fatalf("назначения %+v", m.Purposes)
	}
	if len(m.Models) != 3 || m.Models[0].Model != "sonnet" || m.Models[0].Cost != 2*usd || m.Models[0].Calls != 2 || m.Models[2].Model != "local" || m.Models[2].Priced {
		t.Fatalf("модели %+v", m.Models)
	}
}

// Первый день месяца: прогноз — расход дня на весь месяц; пустой журнал — нули.
func TestMoney_firstDayAndEmpty(t *testing.T) {
	now := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
	m := usage.NewMoney(nil, now, priceBook{})
	if m.Spent != 0 || m.Forecast != 0 || m.DaysPassed != 1 || m.DaysInMonth != 28 || len(m.Days) != 1 {
		t.Fatalf("%+v", m)
	}
}

// Облачная речь со счетом шлюза в деньгах (#478, замер 07.11): цена вызова
// идет в расход как есть, цена модели не нужна, и вызов не считается «без счета».
func TestMoney_billedCalls(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	listen, err := domain.NewBilledModelCall(time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), domain.CallListen, "openai/gpt-4o-mini-transcribe", 96_250_000, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	speak, err := domain.NewBilledModelCall(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), domain.CallSpeak, "google/gemini-3.8-flash-tts", 335_000_000, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	m := usage.NewMoney([]domain.ModelCall{listen, speak}, now, priceBook{})
	if m.Spent != 431_250_000 || m.Uncounted != 0 || m.Unpriced != 0 {
		t.Fatalf("Spent %d, без счета %d, без цены %d", m.Spent, m.Uncounted, m.Unpriced)
	}
	if m.Days[6] != 96_250_000 || m.Days[5] != 335_000_000 {
		t.Fatalf("по дням %v", m.Days)
	}
	if len(m.Models) != 2 || m.Models[0].Model != "google/gemini-3.8-flash-tts" || m.Models[0].Cost != 335_000_000 || !m.Models[0].Priced || m.Models[0].Counted != 0 || m.Models[0].Billed != 1 {
		t.Fatalf("модели %+v", m.Models)
	}
	if len(m.Purposes) != 2 || m.Purposes[0].Purpose != domain.CallSpeak {
		t.Fatalf("назначения %+v", m.Purposes)
	}
}

// Вызовы одной модели со счетом шлюза и без него (отказ, ответ без цены):
// «цена известна» не зависит от того, какой вызов в журнале последний (ревью #507).
func TestMoney_billedPricedRegardlessOfOrder(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	billed, err := domain.NewBilledModelCall(time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), domain.CallSpeak, "tts", 335_000_000, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	failed := call(t, time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC), domain.CallSpeak, "tts", 0, 0, false)
	for _, calls := range [][]domain.ModelCall{{billed, failed}, {failed, billed}} {
		m := usage.NewMoney(calls, now, priceBook{})
		if len(m.Models) != 1 || !m.Models[0].Priced || m.Models[0].Billed != 1 || m.Models[0].Cost != 335_000_000 || m.Uncounted != 1 {
			t.Fatalf("модели %+v, без счета %d", m.Models, m.Uncounted)
		}
	}
}

// Расход по чатам (#541): чаты от дорогих к дешевым, вызовы вне чата в список
// не входят, неизвестная цена расходом чата не считается, а сумма чатов и
// вызовов вне чата равна общему расходу.
func TestMoney_Chats(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	book := priceBook{"haiku": mustPrice(t, "1", "5")}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	calls := []domain.ModelCall{
		call(t, day(2), domain.CallAnswer, "haiku", 1_000_000, 0, true).WithChat("aaaaaaaaaaaaaaaa"),                                        // $1
		call(t, day(3), domain.CallSearch, "haiku", 1_000_000, 0, true).WithChat("aaaaaaaaaaaaaaaa"),                                        // $1
		call(t, day(4), domain.CallAnswer, "haiku", 3_000_000, 0, true).WithChat("bbbbbbbbbbbbbbbb"),                                        // $3
		call(t, day(5), domain.CallExtract, "haiku", 500_000, 0, true),                                                                      // вне чата, $0,5
		call(t, day(6), domain.CallAnswer, "haiku", 0, 0, false).WithChat("cccccccccccccccc"),                                               // без счета
		call(t, day(7), domain.CallAnswer, "other", 1_000_000, 0, true).WithChat("dddddddddddddddd"),                                        // без цены
		call(t, time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), domain.CallAnswer, "haiku", 9_000_000, 0, true).WithChat("eeeeeeeeeeeeeeee"), // прошлый месяц
	}
	m := usage.NewMoney(calls, now, book)
	const usd = int64(1_000_000_000_000)
	if len(m.Chats) != 4 {
		t.Fatalf("чатов %d: %+v", len(m.Chats), m.Chats)
	}
	if got := m.Chats[0]; got.Chat != "bbbbbbbbbbbbbbbb" || got.Cost != 3*usd || got.Calls != 1 {
		t.Errorf("первый = %+v", got)
	}
	if got := m.Chats[1]; got.Chat != "aaaaaaaaaaaaaaaa" || got.Cost != 2*usd || got.Calls != 2 {
		t.Errorf("второй = %+v", got)
	}
	var sum int64
	for _, c := range m.Chats {
		sum += c.Cost
	}
	if sum+usd/2 != m.Spent {
		t.Errorf("сумма чатов %d + вне чатов %d != расход %d", sum, usd/2, m.Spent)
	}
}
