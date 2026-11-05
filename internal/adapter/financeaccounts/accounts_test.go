package financeaccounts_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financeaccounts"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

var clock = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }

func money(t *testing.T, s string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Записанное читается обратно тем же: имя, остаток до копейки, день,
// валюта с курсом и момент подтверждения.
func TestRoundTrip(t *testing.T) {
	yekat, err := time.LoadLocation("Asia/Yekaterinburg")
	if err != nil {
		t.Fatal(err)
	}
	// В UTC еще 5-е. Доли секунды важны: трату сравнивают с моментом по
	// миллисекундам ULID, и момент, урезанный до секунды, вычел бы трату,
	// записанную в ту же секунду до подтверждения.
	day := time.Date(2026, 11, 6, 1, 30, 0, 123456789, yekat)
	sber, err := domain.NewAccount("Сбербанк", money(t, "1234.56"), day, clock)
	if err != nil {
		t.Fatal(err)
	}
	usd, _ := domain.NewCurrency("USD")
	rate, _ := domain.NewRate(money(t, "84.28"))
	wise, err := domain.NewForeignAccount("Долг → Wise", money(t, "-10"), usd, rate, time.Time{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	in := financeaccounts.Book{Accounts: []domain.Account{sber, wise}, Confirmations: finance.Confirmations{"Сбербанк": day}}
	raw, err := financeaccounts.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := financeaccounts.Parse("users/u1/fin/accounts.json", raw, clock)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, raw)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("%d accounts\n%s", len(out.Accounts), raw)
	}
	a, b := out.Accounts[0], out.Accounts[1]
	if a.Bank() != "Сбербанк" || a.Balance().String() != "1234.56" || a.Updated().Format(time.DateOnly) != "2026-11-06" || !a.Currency().IsBase() {
		t.Errorf("Сбербанк read back as %q %s %s %s\n%s", a.Bank(), a.Balance(), a.Updated().Format(time.DateOnly), a.Currency(), raw)
	}
	per, ok := b.Rate().PerUnit()
	if b.Bank() != "Долг → Wise" || b.Balance().String() != "-10.00" || b.Currency().Code() != "USD" || !ok || per.String() != "84.28" || !b.Updated().IsZero() {
		t.Errorf("Wise read back as %q %s %s %v %v\n%s", b.Bank(), b.Balance(), b.Currency(), b.Rate(), b.Updated(), raw)
	}
	if m, ok := out.Confirmations.At("Сбербанк"); !ok || !m.Equal(day) {
		t.Errorf("confirmation %v %v, want %v", m, ok, day)
	}
	if _, ok := out.Confirmations.At("Долг → Wise"); ok {
		t.Errorf("Wise got a confirmation it did not have")
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name, raw string
		accounts  int
		wantErr   string // часть ошибки; "" — без ошибки
	}{
		{"empty document", "", 0, ""},
		{"no accounts", `{"accounts":[]}`, 0, ""},
		{"broken json names the document", `{`, 0, "users/u1/fin/accounts.json"},
		{"bad balance", `{"accounts":[{"bank":"Сбербанк","balance":"abc"}]}`, 0, "Сбербанк"},
		{"bad day", `{"accounts":[{"bank":"Сбербанк","balance":"1","updated":"06.11.2026"}]}`, 0, "Сбербанк"},
		{"rate without currency", `{"accounts":[{"bank":"Сбербанк","balance":"1","rate":"84.28"}]}`, 0, "Сбербанк"},
		{"two spellings of one account", `{"accounts":[{"bank":"Сбербанк","balance":"1"},{"bank":"сбербанк ","balance":"2"}]}`, 0, "сбербанк"},
		{"unknown field", `{"accounts":[{"bank":"Сбербанк","balance":"1","amount":"2"}]}`, 0, "amount"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := financeaccounts.Parse("users/u1/fin/accounts.json", []byte(c.raw), clock)
			if c.wantErr == "" {
				if err != nil || len(b.Accounts) != c.accounts {
					t.Fatalf("%d accounts, %v", len(b.Accounts), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err %v, want it to name %q", err, c.wantErr)
			}
			// Битый документ — не ответ на запрос: доменная ошибка «счет уже есть»
			// превратилась бы у API в 409 вместо сбоя хранилища.
			if !errors.Is(err, financeaccounts.ErrMalformed) || errors.Is(err, domain.ErrAccountExists) {
				t.Errorf("a broken document is not ErrMalformed alone: %v", err)
			}
		})
	}
}
