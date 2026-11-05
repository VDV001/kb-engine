package domain_test

import (
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func exchangeParams(t *testing.T, amount string, r domain.ReceivedParams) domain.TransactionParams {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatal(err)
	}
	return domain.TransactionParams{
		ID: "01EXCH", Kind: domain.KindExpense, Date: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), Amount: m,
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк", Received: r,
		Now: func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) },
	}
}

func money(t *testing.T, s string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Вторая сторона перевода между своими счетами. Обмен валюты — частный
// случай: курс не вводится, а вычисляется из двух сумм и остается в записи
// навсегда — фактический курс банка вместе с комиссией, без усреднения по
// истории (решение владельца 05.11).
func TestReceived(t *testing.T) {
	usd, _ := domain.NewCurrency("USD")
	eur, _ := domain.NewCurrency("EUR")
	cases := []struct {
		name     string
		p        func(t *testing.T) domain.TransactionParams
		wantErr  bool
		wantRate string // "" — курса нет
	}{
		{"rubles to dollars", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: " Wise "})
		}, false, "90.00"},
		{"a rate with the bank's commission is not rounded to a nice number", func(t *testing.T) domain.TransactionParams {
			// 91,3765 — округление до копейки, а не отбрасывание: 91,38.
			return exchangeParams(t, "9137.65", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "Wise"})
		}, false, "91.38"},
		{"dollars back to rubles: still rubles per dollar", func(t *testing.T) domain.TransactionParams {
			p := exchangeParams(t, "100", domain.ReceivedParams{Amount: money(t, "8850"), Account: "Сбербанк"})
			p.Account, p.Currency = "Wise", usd
			return p
		}, false, "88.50"},
		// Валюта дешевле рубля: курс в копейках за единицу округлился бы до
		// 0,01 (ошибка 33%) или до нуля. Дробь точная.
		{"rubles to sums: a rate below a kopeck stays exact", func(t *testing.T) domain.TransactionParams {
			uzs, _ := domain.NewCurrency("UZS")
			return exchangeParams(t, "10000", domain.ReceivedParams{Amount: money(t, "1333000"), Currency: uzs, Account: "Kapitalbank"})
		}, false, "0.007502"},
		{"between two ruble accounts: no rate", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "5000", domain.ReceivedParams{Amount: money(t, "5000"), Account: "Т-Банк"})
		}, false, ""},
		{"dollars to euros: no rate to the book's currency", func(t *testing.T) domain.TransactionParams {
			p := exchangeParams(t, "100", domain.ReceivedParams{Amount: money(t, "92"), Currency: eur, Account: "Revolut"})
			p.Account, p.Currency = "Wise", usd
			return p
		}, false, ""},
		{"only a transfer to self has a second side", func(t *testing.T) domain.TransactionParams {
			p := exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "Wise"})
			p.Category, p.Subcategory = "Еда", "Продукты"
			return p
		}, true, ""},
		{"the second side needs an account", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "  "})
		}, true, ""},
		{"not to the same account", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "сбербанк"})
		}, true, ""},
		{"the source needs an account", func(t *testing.T) domain.TransactionParams {
			p := exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "Wise"})
			p.Account = ""
			return p
		}, true, ""},
		{"nothing received is not a side", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "0"), Currency: usd, Account: "Wise"})
		}, true, ""},
		{"a negative amount received", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "9000", domain.ReceivedParams{Amount: money(t, "-100"), Currency: usd, Account: "Wise"})
		}, true, ""},
		{"a negative amount sent", func(t *testing.T) domain.TransactionParams {
			return exchangeParams(t, "-9000", domain.ReceivedParams{Amount: money(t, "100"), Currency: usd, Account: "Wise"})
		}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := domain.NewTransaction(c.p(t))
			if c.wantErr {
				if !errors.Is(err, domain.ErrInvalidTransaction) {
					t.Fatalf("err %v, want ErrInvalidTransaction", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r, ok := tx.Received()
			if !ok || r.Account() == "" || r.Account() != strings.TrimSpace(r.Account()) {
				t.Fatalf("received %+v %v", r, ok)
			}
			rate, ok := tx.ExchangeRate()
			got := ""
			if ok {
				got = rate.FloatString(2)
				if rate.Cmp(big.NewRat(1, 1)) < 0 {
					got = rate.FloatString(6)
				}
			}
			if got != c.wantRate {
				t.Errorf("rate %q, want %q", got, c.wantRate)
			}
		})
	}
}

// Обычный перевод себе без второй стороны остается каким был: старые записи
// журнала ее не несут.
func TestTransferWithoutASecondSide(t *testing.T) {
	tx, err := domain.NewTransaction(exchangeParams(t, "9000", domain.ReceivedParams{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tx.Received(); ok {
		t.Fatal("a transfer without a second side reports one")
	}
	if _, ok := tx.ExchangeRate(); ok {
		t.Fatal("a transfer without a second side reports a rate")
	}
}
