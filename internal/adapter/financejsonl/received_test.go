package financejsonl_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Вторая сторона обмена записывается в журнал и читается обратно целиком:
// сумма, валюта, счет. Строки без нее — как раньше, без пустого поля.
func TestReceivedRoundTrips(t *testing.T) {
	usd, _ := domain.NewCurrency("USD")
	exch := record(t, "01EXCH", domain.TransactionParams{
		Kind: domain.KindExpense, Date: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), Amount: domain.NewMoney(900000),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
		Received: domain.ReceivedParams{Amount: domain.NewMoney(10000), Currency: usd, Account: "Wise"},
	})
	var enc bytes.Buffer
	if err := financejsonl.Encode(&enc, []finance.Record{exch, expense(t, "01PLAIN")}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(enc.String()), "\n")
	if !strings.Contains(lines[0], `"received":{"amount":"100.00","currency":"USD","account":"Wise"}`) {
		t.Errorf("the exchange line: %s", lines[0])
	}
	if strings.Contains(lines[1], "received") {
		t.Errorf("a plain line carries an empty second side: %s", lines[1])
	}
	got, err := financejsonl.Parse("users/u1/fin/journal.jsonl", bytes.NewReader(enc.Bytes()), loadedAt)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := got[0].Transaction().Received()
	if !ok || r.Amount().String() != "100.00" || r.Currency().Code() != "USD" || r.Account() != "Wise" {
		t.Fatalf("read back %+v %v", r, ok)
	}
	if _, ok := got[1].Transaction().Received(); ok {
		t.Fatal("a plain line read back with a second side")
	}
}

// Битая вторая сторона — отказ строки с номером, как любая битая строка.
func TestReceivedRefusals(t *testing.T) {
	for _, line := range []string{
		`{"id":"01X","kind":"expense","date":"2026-03-29","amount":"9000.00","category":"Прочее","subcategory":"Перевод себе","account":"Сбербанк","received":{"amount":"abc","account":"Wise"},"rev":1,"updated_at":"2026-03-29T00:00:00Z"}`,
		`{"id":"01X","kind":"expense","date":"2026-03-29","amount":"9000.00","category":"Прочее","subcategory":"Перевод себе","account":"Сбербанк","received":{"amount":"100","currency":"US","account":"Wise"},"rev":1,"updated_at":"2026-03-29T00:00:00Z"}`,
		`{"id":"01X","kind":"expense","date":"2026-03-29","amount":"9000.00","category":"Прочее","subcategory":"Перевод себе","account":"Сбербанк","received":{"amount":"100","account":"Wise","rate":"90"},"rev":1,"updated_at":"2026-03-29T00:00:00Z"}`,
	} {
		if _, err := financejsonl.Parse("j", strings.NewReader(line+"\n"), loadedAt); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%s\n→ %v, want a refusal naming line 1", line, err)
		}
	}
}
