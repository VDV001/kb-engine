package domain

import (
	"fmt"
	"math/big"
	"strings"
)

// Received is the other side of a transfer between the owner's own accounts:
// сколько, в какой валюте и на какой счет пришло. Обмен валюты — такой перевод:
// списано 9 000 ₽ со Сбербанка, получено 100 $ на Wise.
type Received struct {
	amount   Money
	currency Currency
	account  string
}

// ReceivedParams are the fields of Received as a caller names them.
type ReceivedParams struct {
	Amount   Money
	Currency Currency
	Account  string
}

// Amount is how much arrived, in Currency.
func (r Received) Amount() Money { return r.amount }

// Currency is the unit Amount is in; the zero value is the book's currency.
func (r Received) Currency() Currency { return r.currency }

// Account is where the money arrived.
func (r Received) Account() string { return r.account }

// withReceived attaches the second side; the zero value means there is none.
func (t Transaction) withReceived(p ReceivedParams) (Transaction, error) {
	if p == (ReceivedParams{}) {
		return t, nil
	}
	r, err := t.newReceived(p)
	if err != nil {
		return Transaction{}, err
	}
	t.received = &r
	return t, nil
}

// newReceived validates the second side against the transfer it belongs to.
//
// Вторая сторона есть только у перевода себе со счета: у обычной траты деньги
// ушли из дома, у дохода нет счета, и прибавлять получение некуда. Получение
// на тот же счет, нулевое или отрицательное — не перевод, а опечатка.
func (t Transaction) newReceived(p ReceivedParams) (Received, error) {
	bad := func(why string) (Received, error) {
		return Received{}, fmt.Errorf("%w: received: %s", ErrInvalidTransaction, why)
	}
	switch account := strings.TrimSpace(p.Account); {
	case !t.IsExpense() || !t.IsInternalTransfer():
		return bad("only a transfer to self has a second side")
	case t.account == "":
		return bad("the transfer needs the account it was sent from")
	case account == "":
		return bad("the account it arrived on is required")
	case SameAccountName(account, t.account):
		return bad("sent and received on the same account")
	case p.Amount.Kopecks() <= 0 || t.amount.Kopecks() <= 0:
		return bad("both amounts must be positive")
	default:
		return Received{amount: p.Amount, currency: p.Currency, account: account}, nil
	}
}

// CheckReceivedAccountCurrency applies the account-currency rule to the second
// side: доллары, пришедшие на рублевый счет, — опечатка в валюте или в счете.
func (t Transaction) CheckReceivedAccountCurrency(acc Account) error {
	r, ok := t.Received()
	if !ok || r.currency == acc.Currency() {
		return nil
	}
	return fmt.Errorf("%w: received %s on %q, which is held in %s",
		ErrCurrencyMismatch, r.currency.Code(), acc.Bank(), acc.Currency().Code())
}

// Received returns the other side of the transfer, false when there is none.
func (t Transaction) Received() (Received, bool) {
	if t.received == nil {
		return Received{}, false
	}
	return *t.received, true
}

// ExchangeRate is how much of the book's currency one foreign unit cost in
// this exchange — из двух сумм записи, то есть фактический курс банка вместе с
// комиссией. Только когда ровно одна сторона в валюте книги: у перевода между
// рублевыми счетами курса нет, у обмена долларов на евро нет курса к рублю.
// Округление до копейки — для показа; суммы в записи остаются точными.
func (t Transaction) ExchangeRate() (Rate, bool) {
	r, ok := t.Received()
	if !ok || t.currency.IsBase() == r.currency.IsBase() {
		return Rate{}, false
	}
	base, foreign := t.amount, r.amount
	if !t.currency.IsBase() {
		base, foreign = r.amount, t.amount
	}
	// Копейки базовой валюты за единицу: base·100 / foreign, половина вверх.
	// big.Int — потому что base·100 у очень большой суммы вышел бы за int64.
	num := new(big.Int).Mul(big.NewInt(base.Kopecks()), big.NewInt(100))
	den := big.NewInt(foreign.Kopecks())
	num.Add(num, new(big.Int).Quo(den, big.NewInt(2)))
	per := new(big.Int).Quo(num, den)
	if !per.IsInt64() {
		return Rate{}, false
	}
	rate, err := NewRate(NewMoney(per.Int64()))
	if err != nil {
		return Rate{}, false
	}
	return rate, true
}
