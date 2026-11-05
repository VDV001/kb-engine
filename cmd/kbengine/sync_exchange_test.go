package main

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// В листе «Расходы» книги нет колонок для второй стороны обмена: синк молча
// записал бы строку без нее, а правка этой строки в Excel стерла бы ее из
// журнала. Поэтому журнал с обменом с книгой не синхронизируется — отказ с
// причиной, книга и журнал нетронуты.
func TestRun_finSync_refusesAJournalWithAnExchange(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	recs, err := financejsonl.Load(ledger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	usd, _ := domain.NewCurrency("USD")
	m := func(s string) domain.Money { v, _ := domain.ParseMoney(s); return v }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), Amount: m("9000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
		Received: &finance.ReceivedAdd{Amount: m("100"), Currency: &usd, Account: "Wise"},
	}, newULID, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := financejsonl.Save(ledger, append(recs, rec), time.Now); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := sync(t, xlsx, ledger)
	if code == 0 || !strings.Contains(stderr, "обмен") {
		t.Fatalf("exit %d, stderr %q: want a refusal that names the exchange", code, stderr)
	}
	after, err := financejsonl.Load(ledger, time.Now)
	if err != nil || len(after) != len(recs)+1 {
		t.Fatalf("the journal changed: %d records, %v", len(after), err)
	}
}
