package main

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/xuri/excelize/v2"
)

// Обмен валюты проходит синк в обе стороны (#432): журнал → книга кладет
// вторую сторону в колонки «Получено», «Валюта получения», «Счет получения»,
// а правка суммы этой строки в Excel возвращается в журнал вместе со второй
// стороной. Раньше журнал с обменом с книгой не синхронизировался вовсе.
func TestRun_finSync_exchangeRoundTrip(t *testing.T) {
	xlsx := workbookWithDollarAccount(t)
	ledger := filepath.Join(filepath.Dir(xlsx), "transactions.jsonl")
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync --init exit = %d, stderr = %s", code, errb.String())
	}
	recs, err := financejsonl.Load(ledger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	usd, _ := domain.NewCurrency("USD")
	m := func(s string) domain.Money { v, _ := domain.ParseMoney(s); return v }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), Amount: m("9000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
		Received: &finance.ReceivedAdd{Amount: m("100"), Currency: &usd, Account: "Банк USD"},
	}, newULID, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := financejsonl.Save(ledger, append(recs, rec), time.Now); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := sync(t, xlsx, ledger); code != 0 || !strings.Contains(stdout, "ledger → workbook") {
		t.Fatalf("журнал → книга: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	row, amountCell := exchangeRow(t, xlsx)
	if row == nil {
		t.Fatal("в книге нет строки с «Получено»")
	}
	if code, stdout, _ := sync(t, xlsx, ledger); code != 0 || !strings.Contains(stdout, "nothing to do") {
		t.Fatalf("второй синк: exit %d, %q — обмен прочитался из книги не тем, что записан", code, stdout)
	}

	// Человек правит сумму обмена в Excel.
	editWorkbook(t, xlsx, "Расходы", amountCell, 9100)
	if code, stdout, stderr := sync(t, xlsx, ledger); code != 0 || !strings.Contains(stdout, "workbook → ledger") {
		t.Fatalf("книга → журнал: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	after, err := financejsonl.Load(ledger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range after {
		tx := r.Transaction()
		if tx.ID() != rec.Transaction().ID() {
			continue
		}
		got, ok := tx.Received()
		if tx.Amount() != m("9100") || !ok || got.Amount() != m("100") || got.Currency().Code() != "USD" || got.Account() != "Банк USD" {
			t.Fatalf("после правки в книге: сумма %v, вторая сторона %+v (%v)", tx.Amount(), got, ok)
		}
		return
	}
	t.Fatal("обмен пропал из журнала")
}

// exchangeRow returns the Расходы row whose Получено is filled, and the cell of
// its amount (column F).
func exchangeRow(t *testing.T, path string) ([]string, string) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows("Расходы")
	if err != nil {
		t.Fatal(err)
	}
	col := -1
	for i, h := range rows[1] {
		if h == "Получено" {
			col = i
		}
	}
	if col < 0 {
		return nil, ""
	}
	for i, r := range rows {
		if i >= 2 && col < len(r) && r[col] != "" {
			return r, "F" + strconv.Itoa(i+1)
		}
	}
	return nil, ""
}
