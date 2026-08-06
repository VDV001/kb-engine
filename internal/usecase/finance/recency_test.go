package finance_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

func recAt(t *testing.T, recorded, date, amount string) finance.Record {
	t.Helper()
	at, err := time.Parse(time.RFC3339, recorded)
	if err != nil {
		t.Fatalf("recorded: %v", err)
	}
	id := ulid.MustNew(ulid.Timestamp(at), ulid.Monotonic(rand.New(rand.NewSource(1)), 0)).String()
	return recWithID(t, id, date, amount)
}

func recWithID(t *testing.T, id, date, amount string) finance.Record {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day, Amount: m, Category: "Еда",
	}, func() string { return id }, func() time.Time { return day })
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

// «Последние записи» в терминале и первая страница журнала в вебе обязаны
// отвечать одинаково на вопрос «что записано последним». До этого терминал
// сортировал по СЫРОМУ id: строка, вписанная в книгу мимо движка, получает
// позиционный id вида expense-rN, а латинская «e» больше любого символа ULID —
// и такая строка вставала наверх списка, хотя момента её записи никто не знает.
func TestSortByRecency(t *testing.T) {
	recs := []finance.Record{
		recAt(t, "2026-08-05T09:00:00Z", "2026-08-05", "100.00"),
		recWithID(t, "expense-r12", "2026-08-05", "200.00"),
		recAt(t, "2026-08-05T18:00:00Z", "2026-08-05", "300.00"),
		recAt(t, "2026-08-04T10:00:00Z", "2026-08-04", "400.00"),
	}

	finance.SortByRecency(recs)

	var got []string
	for _, r := range recs {
		got = append(got, r.Transaction().Amount().String())
	}
	want := []string{"300.00", "100.00", "200.00", "400.00"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок = %v, ожидался %v (внутри дня новее сверху, момент неизвестен — ниже всех известных)", got, want)
		}
	}
}
