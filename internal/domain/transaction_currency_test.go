package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// У записи не было валюты, поэтому трата со счёта в долларах читалась
// рублями: имя счёта говорило одно, число — другое, и итоги складывали доллары
// с рублями. Нулевая валюта — рубль, как у счёта: старый журнал и старая книга
// о валюте не знают, и умолчание обязано быть тем, чем они пользовались.
func TestNewTransaction_currency(t *testing.T) {
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	cases := []struct {
		name     string
		kind     string
		currency domain.Currency
		want     string
		wantBase bool
	}{
		{"без валюты трата рублёвая", domain.KindExpense, domain.Currency{}, "RUB", true},
		{"валюта траты сохраняется", domain.KindExpense, usd, "USD", false},
		{"доход тоже несёт валюту", domain.KindIncome, usd, "USD", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := txParams()
			p.Kind = tc.kind
			if tc.kind == domain.KindIncome {
				p.Category, p.Subcategory, p.Place, p.Account = "", "", "", ""
				p.Source = "Заказчик"
			}
			p.Currency = tc.currency
			tx, err := domain.NewTransaction(p)
			if err != nil {
				t.Fatalf("NewTransaction: %v", err)
			}
			if got := tx.Currency().Code(); got != tc.want {
				t.Errorf("Currency().Code() = %q, want %q", got, tc.want)
			}
			if got := tx.Currency().IsBase(); got != tc.wantBase {
				t.Errorf("Currency().IsBase() = %v, want %v", got, tc.wantBase)
			}
		})
	}
}

// Трата со счёта обязана быть в его валюте. Иначе расчётный остаток вычитает
// доллары из сумов, а итог по счёту складывает одно с другим, и ни одна из двух
// цифр уже не значит ничего. Правило про то, что такое трата со счёта, поэтому
// живёт здесь, а не там, где его впервые встречают.
func TestTransaction_CheckAccountCurrency(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC) }
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	usd, _ := domain.NewCurrency("USD")
	uzs, _ := domain.NewCurrency("UZS")

	rubAcc, err := domain.NewAccount("Первый банк", domain.NewMoney(100000), day, now)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	usdAcc, err := domain.NewForeignAccount("Банк USD", domain.NewMoney(100000), usd, domain.UnknownRate(), day, now)
	if err != nil {
		t.Fatalf("NewForeignAccount: %v", err)
	}

	cases := []struct {
		name     string
		currency domain.Currency
		account  domain.Account
		wantErr  bool
	}{
		{"рубли с рублёвого счёта", domain.Currency{}, rubAcc, false},
		{"доллары с долларового счёта", usd, usdAcc, false},
		{"доллары с рублёвого счёта", usd, rubAcc, true},
		{"рубли с долларового счёта", domain.Currency{}, usdAcc, true},
		{"сумы с долларового счёта", uzs, usdAcc, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := txParams()
			p.Account = tc.account.Bank()
			p.Currency = tc.currency
			tx, err := domain.NewTransaction(p)
			if err != nil {
				t.Fatalf("NewTransaction: %v", err)
			}
			err = tx.CheckAccountCurrency(tc.account)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrCurrencyMismatch) {
					t.Fatalf("CheckAccountCurrency = %v, want ErrCurrencyMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckAccountCurrency = %v, want nil", err)
			}
		})
	}
}
