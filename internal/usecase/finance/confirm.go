package finance

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// BalanceChange is one confirmation of an account's balance: человек посмотрел
// в приложение банка и назвал число.
type BalanceChange struct {
	Bank    string
	Balance domain.Money
	// Create заводит счет, которого еще нет; без него незнакомое имя — отказ.
	Create bool
	// Currency — валюта счета; nil — прежняя (у нового счета — валюта книги).
	// Rate — курс к валюте книги, только вместе с Currency.
	Currency *domain.Currency
	Rate     domain.Rate
}

// ConfirmBalance returns the accounts and confirmations after c.
func ConfirmBalance(accounts []domain.Account, confs Confirmations, c BalanceChange, now func() time.Time) ([]domain.Account, Confirmations, error) {
	return accounts, confs, nil
}
