package finance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Валюта траты берётся у счёта, если человек её не назвал: «потратил 12,30 с
// долларового счёта» — это доллары, и заставлять каждый раз дописывать «USD» значит
// рано или поздно получить доллары, записанные рублями. Названная валюта
// сверяется со счётом доменным правилом, а без книги под рукой (счетов нет)
// подставлять нечего — тогда рубль, как было всегда.

var currencyNow = func() time.Time { return day(2026, time.October, 23) }

func cur(t *testing.T, code string) *domain.Currency {
	t.Helper()
	c, err := domain.NewCurrency(code)
	if err != nil {
		t.Fatalf("NewCurrency(%q): %v", code, err)
	}
	return &c
}

func testAccounts(t *testing.T) []domain.Account {
	t.Helper()
	updated := day(2026, time.October, 20)
	rub, err := domain.NewAccount("Первый банк", domain.NewMoney(100000), updated, currencyNow)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	usd, err := domain.NewForeignAccount("Банк USD", domain.NewMoney(100000), *cur(t, "USD"), domain.UnknownRate(), updated, currencyNow)
	if err != nil {
		t.Fatalf("NewForeignAccount: %v", err)
	}
	return []domain.Account{rub, usd}
}

func TestAdd_currency(t *testing.T) {
	accounts := testAccounts(t)
	cases := []struct {
		name     string
		account  string
		currency *domain.Currency
		accounts []domain.Account
		want     string
		wantErr  error
	}{
		{"валюта не названа — берётся у долларового счёта", "Банк USD", nil, accounts, "USD", nil},
		{"валюта не названа — рублёвый счёт даёт рубль", "Первый банк", nil, accounts, "RUB", nil},
		{"без счёта и валюты — рубль", "", nil, accounts, "RUB", nil},
		{"названная совпадает со счётом", "Банк USD", cur(t, "USD"), accounts, "USD", nil},
		{"рубли с долларового счёта — отказ", "Банк USD", cur(t, "RUB"), accounts, "", domain.ErrCurrencyMismatch},
		{"доллары с рублёвого счёта — отказ", "Первый банк", cur(t, "USD"), accounts, "", domain.ErrCurrencyMismatch},
		{"без книги названная валюта сохраняется", "Банк USD", cur(t, "USD"), nil, "USD", nil},
		{"без книги и без валюты — рубль", "Банк USD", nil, nil, "RUB", nil},
		// Счёт ищется так же, как его ищет весь движок: регистр и пробелы не
		// делают другого счёта. Иначе другое написание теряло бы валюту.
		{"другое написание счёта — та же валюта", "банк  usd", nil, accounts, "USD", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := finance.Add(finance.AddParams{
				Kind:     domain.KindExpense,
				Date:     day(2026, time.October, 22),
				Amount:   domain.NewMoney(1230),
				Category: "Сервисы",
				Account:  tc.account,
				Currency: tc.currency,
				Accounts: tc.accounts,
			}, func() string { return "01CUR" }, currencyNow)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Add = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if got := rec.Transaction().Currency().Code(); got != tc.want {
				t.Errorf("Currency = %s, want %s", got, tc.want)
			}
		})
	}
}

// Та же сумма в другой валюте — другая трата: $10 и 10 ₽ в одном магазине в
// один день не повтор. Без валюты в ключе вторую запись отвергли бы как дубль.
func TestDuplicate_currencyTellsEntriesApart(t *testing.T) {
	existing, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day(2026, time.October, 22), Amount: domain.NewMoney(1000),
		Category: "Еда", Place: "Лавка",
	}, func() string { return "01RUB" }, currencyNow)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	p := finance.AddParams{
		Kind: domain.KindExpense, Date: day(2026, time.October, 22), Amount: domain.NewMoney(1000),
		Category: "Еда", Place: "Лавка",
	}
	if dup := finance.Duplicate([]finance.Record{existing}, p); dup == nil {
		t.Fatal("рублёвый повтор не найден — контроль сломан, тест ниже ничего не значит")
	}
	p.Currency = cur(t, "USD")
	if dup := finance.Duplicate([]finance.Record{existing}, p); dup != nil {
		t.Errorf("$10 приняты за повтор 10 ₽ (%s)", dup.Transaction().ID())
	}
}

func usdRecord(t *testing.T) finance.Record {
	t.Helper()
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day(2026, time.October, 22), Amount: domain.NewMoney(1230),
		Category: "Сервисы", Description: "кредиты", Account: "Банк USD", Currency: cur(t, "USD"),
	}, func() string { return "01USD" }, currencyNow)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

// Правка пересобирает транзакцию из полей. Не перенеси она валюту, любая
// правка заметки молча превратила бы доллары в рубли.
func TestEdit_keepsTheCurrency(t *testing.T) {
	edited, err := finance.Edit(usdRecord(t), finance.EditParams{Description: "кредиты на агентов"}, currencyNow)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got := edited.Transaction().Currency().Code(); got != "USD" {
		t.Errorf("после правки заметки валюта %s, want USD", got)
	}
}

func TestEdit_currency(t *testing.T) {
	accounts := testAccounts(t)
	rub, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day(2026, time.October, 22), Amount: domain.NewMoney(1230),
		Category: "Сервисы", Account: "Первый банк",
	}, func() string { return "01RUBACC" }, currencyNow)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	cases := []struct {
		name    string
		rec     finance.Record
		p       finance.EditParams
		want    string
		wantErr error
	}{
		{"валюта меняется явно", usdRecord(t), finance.EditParams{Currency: cur(t, "UZS")}, "UZS", nil},
		// Сумма записана в одной валюте; новый счёт в другой не делает её суммой
		// в другой валюте. Молча сменить код значило бы превратить 50 долларов в
		// 50 рублей, поэтому без явной валюты (и новой суммы) — отказ.
		{"рубли на долларовый счёт — отказ", rub, finance.EditParams{Account: "Банк USD", Accounts: accounts}, "", domain.ErrCurrencyMismatch},
		{"доллары на рублёвый счёт — отказ", usdRecord(t), finance.EditParams{Account: "Первый банк", Accounts: accounts}, "", domain.ErrCurrencyMismatch},
		{"смена счёта с названной валютой", rub, finance.EditParams{Account: "Банк USD", Currency: cur(t, "USD"), Accounts: accounts}, "USD", nil},
		{"названная валюта против счёта — отказ", rub, finance.EditParams{Currency: cur(t, "USD"), Accounts: accounts}, "", domain.ErrCurrencyMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edited, err := finance.Edit(tc.rec, tc.p, currencyNow)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Edit = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Edit: %v", err)
			}
			if got := edited.Transaction().Currency().Code(); got != tc.want {
				t.Errorf("Currency = %s, want %s", got, tc.want)
			}
		})
	}
}
