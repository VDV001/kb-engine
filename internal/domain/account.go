package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidAccount is returned when account parameters violate an invariant.
var ErrInvalidAccount = errors.New("invalid account")

// Account is a bank balance snapshot as recorded in the ledger.
//
// A balance may legitimately be negative (an overdraft or a credit card), so
// unlike a transaction amount its sign is not constrained.
type Account struct {
	bank    string
	balance Money
	updated time.Time
}

// NewAccount validates and returns an account snapshot. The clock is a
// parameter for the same reason as in NewTransaction: validation must not
// depend on when it happens to run.
func NewAccount(bank string, balance Money, updated time.Time, now func() time.Time) (Account, error) {
	name := strings.TrimSpace(bank)
	if name == "" {
		return Account{}, fmt.Errorf("%w: bank is required", ErrInvalidAccount)
	}
	if now == nil {
		return Account{}, fmt.Errorf("%w: clock is required", ErrInvalidAccount)
	}
	if !updated.IsZero() && updated.After(now()) {
		return Account{}, fmt.Errorf("%w: updated %s is in the future", ErrInvalidAccount, updated.Format(time.DateOnly))
	}
	return Account{bank: name, balance: balance, updated: updated}, nil
}

// Bank returns the account name.
func (a Account) Bank() string { return a.bank }

// Balance returns the recorded balance.
func (a Account) Balance() Money { return a.balance }

// Updated returns when the balance was last confirmed; zero when unknown.
func (a Account) Updated() time.Time { return a.updated }
