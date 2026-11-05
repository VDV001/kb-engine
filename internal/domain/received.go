package domain

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

// Received returns the other side of the transfer, false when there is none.
func (t Transaction) Received() (Received, bool) { return Received{}, false }

// ExchangeRate is how much of the book's currency one foreign unit cost.
func (t Transaction) ExchangeRate() (Rate, bool) { return Rate{}, false }
