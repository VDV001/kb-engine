package finance_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Подтверждение остатка: человек назвал число из приложения банка. Счет
// ищется по имени так же, как везде (регистр, пробелы, ё), подтверждение
// ставит и день, и момент — момент решает спор о тратах того же дня.
func TestConfirmBalance(t *testing.T) {
	yekat, err := time.LoadLocation("Asia/Yekaterinburg")
	if err != nil {
		t.Fatal(err)
	}
	// 01:30 6 ноября в Екатеринбурге — в UTC еще 5-е.
	at := time.Date(2026, 11, 6, 1, 30, 0, 0, yekat)
	now := func() time.Time { return at }
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	sber, err := domain.NewAccount("Сбербанк", mustMoney(t, "1000"), old, now)
	if err != nil {
		t.Fatal(err)
	}
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatal(err)
	}
	rate, err := domain.NewRate(mustMoney(t, "84.28"))
	if err != nil {
		t.Fatal(err)
	}
	wise, err := domain.NewForeignAccount("Wise", mustMoney(t, "10"), usd, rate, old, now)
	if err != nil {
		t.Fatal(err)
	}
	base := []domain.Account{sber, wise}

	cases := []struct {
		name    string
		change  finance.BalanceChange
		wantErr error
		check   func(t *testing.T, accs []domain.Account, confs finance.Confirmations)
	}{
		{"existing account by another spelling", finance.BalanceChange{Bank: " сбербанк ", Balance: mustMoney(t, "777.50")}, nil,
			func(t *testing.T, accs []domain.Account, confs finance.Confirmations) {
				if len(accs) != 2 || accs[0].Bank() != "Сбербанк" || accs[0].Balance().String() != "777.50" {
					t.Fatalf("accounts %v", accs)
				}
				if got := accs[0].Updated().Format(time.DateOnly); got != "2026-11-06" {
					t.Errorf("updated %s, want the person's day 2026-11-06", got)
				}
				if m, ok := confs.At("Сбербанк"); !ok || !m.Equal(at) {
					t.Errorf("confirmation %v %v, want %v", m, ok, at)
				}
				if _, ok := confs.At("Wise"); ok {
					t.Errorf("Wise got a confirmation it did not have")
				}
			}},
		{"foreign account keeps its currency and rate", finance.BalanceChange{Bank: "wise", Balance: mustMoney(t, "12")}, nil,
			func(t *testing.T, accs []domain.Account, _ finance.Confirmations) {
				if accs[1].Currency().Code() != "USD" || !accs[1].Rate().Known() || accs[1].Balance().String() != "12.00" {
					t.Fatalf("Wise %v %v %v", accs[1].Currency(), accs[1].Rate(), accs[1].Balance())
				}
			}},
		{"unknown account is refused and the known ones named", finance.BalanceChange{Bank: "Тинькофф", Balance: mustMoney(t, "1")}, domain.ErrUnknownAccount, nil},
		{"create a new account", finance.BalanceChange{Bank: "Т-Банк", Balance: mustMoney(t, "5"), Create: true}, nil,
			func(t *testing.T, accs []domain.Account, confs finance.Confirmations) {
				if len(accs) != 3 || accs[2].Bank() != "Т-Банк" || !accs[2].Currency().IsBase() {
					t.Fatalf("accounts %v", accs)
				}
				if _, ok := confs.At("Т-Банк"); !ok {
					t.Errorf("a new account without a confirmation moment")
				}
			}},
		{"create a foreign account", finance.BalanceChange{Bank: "Revolut", Balance: mustMoney(t, "3"), Create: true, Currency: &usd, Rate: rate}, nil,
			func(t *testing.T, accs []domain.Account, _ finance.Confirmations) {
				if accs[2].Currency().Code() != "USD" || !accs[2].Rate().Known() {
					t.Fatalf("Revolut %v %v", accs[2].Currency(), accs[2].Rate())
				}
			}},
		{"create an account that is already there", finance.BalanceChange{Bank: "СБЕРБАНК", Balance: mustMoney(t, "1"), Create: true}, domain.ErrAccountExists, nil},
		{"rate without a currency", finance.BalanceChange{Bank: "Сбербанк", Balance: mustMoney(t, "1"), Rate: rate}, domain.ErrInvalidAccount, nil},
		{"empty name", finance.BalanceChange{Bank: "  ", Balance: mustMoney(t, "1"), Create: true}, domain.ErrInvalidAccount, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := append([]domain.Account(nil), base...)
			confs := finance.Confirmations{}
			accs, got, err := finance.ConfirmBalance(in, confs, c.change, now)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err %v, want %v", err, c.wantErr)
				}
				if errors.Is(c.wantErr, domain.ErrUnknownAccount) && !strings.Contains(err.Error(), "Сбербанк") {
					t.Errorf("the refusal does not name the known accounts: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, accs, got)
			if in[0].Balance().String() != "1000.00" || len(confs) != 0 {
				t.Errorf("the input was changed in place")
			}
		})
	}
}

// Момент, записанный под другим написанием имени, заменяется, а не остается
// рядом: два момента одного счета — и расчет взял бы случайный из них.
func TestConfirmBalanceReplacesTheMomentOfAnotherSpelling(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
	sber, err := domain.NewAccount("Сбербанк", mustMoney(t, "1000"), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), now)
	if err != nil {
		t.Fatal(err)
	}
	old := finance.Confirmations{"сбербанк ": time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	_, confs, err := finance.ConfirmBalance([]domain.Account{sber}, old, finance.BalanceChange{Bank: "Сбербанк", Balance: mustMoney(t, "5")}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(confs) != 1 {
		t.Fatalf("%d moments for one account: %v", len(confs), confs)
	}
	if m, _ := confs.At("Сбербанк"); !m.Equal(now()) {
		t.Fatalf("moment %v, want %v", m, now())
	}
	if len(old) != 1 {
		t.Errorf("the input confirmations were changed in place")
	}
}
