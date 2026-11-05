package finance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

func foreignAt(t *testing.T, bank, code, amount, rate, confirmed string) domain.Account {
	t.Helper()
	cur, err := domain.NewCurrency(code)
	if err != nil {
		t.Fatal(err)
	}
	r, err := domain.NewRate(mustMoney(t, rate))
	if err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse(time.DateOnly, confirmed)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := domain.NewForeignAccount(bank, mustMoney(t, amount), cur, r, day, func() time.Time { return day })
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func exchange(t *testing.T, accounts []domain.Account, date, sent, received, to string, currency *domain.Currency) (finance.Record, error) {
	t.Helper()
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatal(err)
	}
	return finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day, Amount: mustMoney(t, sent),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк", Accounts: accounts,
		Received: &finance.ReceivedAdd{Amount: mustMoney(t, received), Currency: currency, Account: to},
	}, func() string { return "01EXCH" + date }, func() time.Time { return day.Add(12 * time.Hour) })
}

// Обмен двигает деньги между своими счетами: рубли уходят со Сбербанка,
// доллары приходят на Wise. Тратой он не считается (перевод себе), курс
// остается в записи.
func TestExchangeMovesMoneyBetweenAccounts(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "10000.00", "2026-08-04"),
		foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04"),
	}
	rec, err := exchange(t, accounts, "2026-08-05", "9000", "100", "wise", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := rec.Transaction()
	r, ok := tx.Received()
	if !ok || r.Currency().Code() != "USD" {
		t.Fatalf("received %+v %v: the currency is taken from the account", r, ok)
	}
	if rate, ok := tx.ExchangeRate(); !ok {
		t.Fatal("no rate")
	} else if per, _ := rate.PerUnit(); per.String() != "90.00" {
		t.Fatalf("rate %s, want 90.00 — from the two amounts, not the account's 80", per)
	}
	balances := finance.CurrentBalances(accounts, []domain.Transaction{tx}, nil)
	got := map[string]string{}
	for _, b := range balances {
		got[b.Bank] = b.Current.String()
	}
	if got["Сбербанк"] != "1000.00" || got["Wise"] != "110.00" {
		t.Fatalf("balances %v, want Сбербанк 1000.00 and Wise 110.00", got)
	}
	if s := finance.Summarize([]finance.Record{rec}); !s.Expenses.IsZero() {
		t.Fatalf("an exchange counted as spending: %s", s.Expenses)
	}
}

// Получение до подтверждения счета получения банк уже учел: прибавлять его
// второй раз нельзя — тем же правилом, что у списания.
func TestExchangeBeforeTheConfirmationIsNotAddedTwice(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "10000.00", "2026-08-01"),
		foreignAt(t, "Wise", "USD", "110.00", "80", "2026-08-10"),
	}
	rec, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range finance.CurrentBalances(accounts, []domain.Transaction{rec.Transaction()}, nil) {
		if b.Bank == "Wise" && b.Current.String() != "110.00" {
			t.Fatalf("Wise %s, want 110.00: the bank already counted it", b.Current)
		}
	}
}

func TestExchangeRefusals(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "10000.00", "2026-08-04"),
		foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04"),
	}
	eur, _ := domain.NewCurrency("EUR")
	if _, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Тинькофф", nil); !errors.Is(err, domain.ErrUnknownAccount) {
		t.Errorf("an unknown account to receive on: %v, want ErrUnknownAccount — the money would vanish from the balances", err)
	}
	if _, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", &eur); !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Errorf("euros onto a dollar account: %v, want ErrCurrencyMismatch", err)
	}
	// Без счетов (книга не под рукой) сверять не с чем: валюта — названная.
	rec, err := exchange(t, nil, "2026-08-05", "9000", "100", "Wise", &eur)
	if err != nil {
		t.Fatalf("without accounts: %v", err)
	}
	if r, _ := rec.Transaction().Received(); r.Currency().Code() != "EUR" {
		t.Errorf("without accounts the named currency is kept: %s", r.Currency())
	}
}

// Правка суммы обмена не стирает его вторую сторону: запись пересобирается
// из полей, и забытое поле пропало бы молча.
func TestEditKeepsTheSecondSide(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "10000.00", "2026-08-04"),
		foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04"),
	}
	rec, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", nil)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := finance.Edit(rec, finance.EditParams{Amount: mustMoney(t, "9100")}, func() time.Time { return time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	r, ok := edited.Transaction().Received()
	if !ok || r.Amount().String() != "100.00" || r.Account() != "Wise" {
		t.Fatalf("after an edit the second side is %+v %v", r, ok)
	}
}
