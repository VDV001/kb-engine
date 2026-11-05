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
func BalancesByCurrency(balances []AccountBalance) []BalanceTotal {
	return nil
}
