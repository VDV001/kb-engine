package finance_test

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

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
	// Имя — как оно записано у счета, а не как набрано («wise»).
	if r.Account() != "Wise" {
		t.Fatalf("received on %q, want the account's own spelling Wise", r.Account())
	}
	if rate, ok := tx.ExchangeRate(); !ok {
		t.Fatal("no rate")
	} else if rate.FloatString(2) != "90.00" {
		t.Fatalf("rate %s, want 90.00 — from the two amounts, not the account's 80", rate.FloatString(2))
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

// Получение в день подтверждения счета получения не прибавляется никогда:
// подтвержденное число могло его уже включать, а прибавить второй раз — значит
// завысить остаток. Расчет может только занижать (правило 24, знак обратный).
func TestExchangeOnTheConfirmationDayIsNeverAddedTwice(t *testing.T) {
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	confirmedAt := day.Add(10 * time.Hour)
	recordedAt := day.Add(11 * time.Hour) // записан после подтверждения, но банк мог уже показать
	usd, _ := domain.NewCurrency("USD")
	rate, _ := domain.NewRate(mustMoney(t, "80"))
	wise, err := domain.NewForeignAccount("Wise", mustMoney(t, "110"), usd, rate, day, func() time.Time { return day })
	if err != nil {
		t.Fatal(err)
	}
	id := ulid.MustNew(ulid.Timestamp(recordedAt), ulid.Monotonic(rand.New(rand.NewSource(1)), 0)).String()
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: id, Kind: domain.KindExpense, Date: day, Amount: mustMoney(t, "9000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
		Received: domain.ReceivedParams{Amount: mustMoney(t, "100"), Currency: usd, Account: "Wise"},
		Now:      func() time.Time { return recordedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	b := finance.CurrentBalances([]domain.Account{wise}, []domain.Transaction{tx}, finance.Confirmations{"Wise": confirmedAt})
	if b[0].Current.String() != "110.00" {
		t.Fatalf("Wise %s, want 110.00: on the confirmation day an arrival may already be in the number", b[0].Current)
	}
}

// Два разных обмена на одну рублевую сумму в один день — не повтор: их
// различает вторая сторона.
func TestTwoExchangesOfOneAmountAreNotARepeat(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "100000.00", "2026-08-04"),
		foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04"),
		foreignAt(t, "Revolut", "EUR", "10.00", "95", "2026-08-04"),
	}
	first, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", nil)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	p := finance.AddParams{
		Kind: domain.KindExpense, Date: day, Amount: mustMoney(t, "9000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк", Accounts: accounts,
	}
	p.Received = &finance.ReceivedAdd{Amount: mustMoney(t, "95"), Account: "Revolut"}
	if dup := finance.Duplicate([]finance.Record{first}, p); dup != nil {
		t.Fatalf("an exchange into Revolut is reported as a repeat of the one into Wise")
	}
	p.Received = &finance.ReceivedAdd{Amount: mustMoney(t, "100"), Account: "wise"}
	if dup := finance.Duplicate([]finance.Record{first}, p); dup == nil {
		t.Fatalf("the same exchange again is not reported as a repeat")
	}
}

// У обмена известны оба счета: со счета, которого нет, ничего не спишется, а
// получение прибавится — обмен создал бы деньги из воздуха.
func TestExchangeFromAnUnknownAccountIsRefused(t *testing.T) {
	accounts := []domain.Account{foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04")}
	if _, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", nil); !errors.Is(err, domain.ErrUnknownAccount) {
		t.Fatalf("an exchange from an unknown Сбербанк: %v, want ErrUnknownAccount", err)
	}
}

// Правка обмена сверяет и вторую сторону с ее счетом.
func TestEditChecksTheSecondSideCurrency(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "10000.00", "2026-08-04"),
		foreignAt(t, "Wise", "USD", "10.00", "80", "2026-08-04"),
	}
	rec, err := exchange(t, accounts, "2026-08-05", "9000", "100", "Wise", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC) }
	changed := []domain.Account{accounts[0], foreignAt(t, "Wise", "EUR", "10.00", "95", "2026-08-04")}
	if _, err := finance.Edit(rec, finance.EditParams{Amount: mustMoney(t, "9100"), Accounts: changed}, now); !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("dollars received on what is now a euro account: %v, want ErrCurrencyMismatch", err)
	}
}
