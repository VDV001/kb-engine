package finance

import "github.com/daniil/kb-engine/internal/domain"

// BalanceTotal is how much money one kind of account holds in one currency.
type BalanceTotal struct {
	Group     string
	Currency  string
	Confirmed domain.Money
	Current   domain.Money
}

// BalancesByCurrency sums the balances per kind of account and currency.
//
// Валюты не складываются друг с другом и не пересчитываются по курсу счета
// (решение владельца 05.11): курс меняется, и любая сумма «в рублях» по нему —
// оценка, а не факт. Род счета отдельно, как у TotalsByGroup, чтобы долг не
// вычитался из денег на карте. Порядок — как роды и валюты встретились в книге.
func BalancesByCurrency(balances []AccountBalance) []BalanceTotal {
	type key struct{ group, currency string }
	var out []BalanceTotal
	at := map[key]int{}
	for _, b := range balances {
		group, _ := domain.SplitAccountName(b.Bank) // из имени, как у TotalsByGroup
		currency := b.Currency
		if currency == "" {
			currency = domain.Currency{}.Code() // собранный руками баланс — валюта книги
		}
		k := key{group, currency}
		i, seen := at[k]
		if !seen {
			out = append(out, BalanceTotal{Group: group, Currency: currency})
			i = len(out) - 1
			at[k] = i
		}
		out[i].Confirmed = out[i].Confirmed.Add(b.Confirmed)
		out[i].Current = out[i].Current.Add(b.Current)
	}
	return out
}
