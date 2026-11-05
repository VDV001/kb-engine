package finance

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// BalanceChange is one confirmation of an account's balance: человек посмотрел
// в приложение банка и назвал число.
type BalanceChange struct {
	Bank    string
	Balance domain.Money
	// Create заводит счет, которого еще нет; без него незнакомое имя — отказ:
	// опечатка и новый счет снаружи неотличимы, решает человек.
	Create bool
	// Currency — валюта счета; nil — прежняя (у нового счета — валюта книги).
	// Rate — курс к валюте книги, только вместе с Currency.
	Currency *domain.Currency
	Rate     domain.Rate
}

// ConfirmBalance returns the accounts and confirmations after c: остаток,
// день подтверждения и его момент — из часов now, то есть в поясе человека.
// Счет ищется как везде (SameAccountName), имя остается прежним. Входы не
// меняются: вызывающий пишет результат условной записью и при конфликте зовет
// снова по свежему.
func ConfirmBalance(accounts []domain.Account, confs Confirmations, c BalanceChange, now func() time.Time) ([]domain.Account, Confirmations, error) {
	i := slices.IndexFunc(accounts, func(a domain.Account) bool { return domain.SameAccountName(a.Bank(), c.Bank) })
	switch {
	case c.Create && i >= 0:
		return nil, nil, fmt.Errorf("%w: %q", domain.ErrAccountExists, accounts[i].Bank())
	case !c.Create && i < 0:
		known := make([]string, 0, len(accounts))
		for _, a := range accounts {
			known = append(known, a.Bank())
		}
		return nil, nil, fmt.Errorf("%w: %q — known: %s", domain.ErrUnknownAccount, c.Bank, strings.Join(known, ", "))
	}

	name, currency, rate := c.Bank, domain.Currency{}, domain.UnknownRate()
	if i >= 0 {
		name, currency, rate = accounts[i].Bank(), accounts[i].Currency(), accounts[i].Rate()
	}
	switch {
	case c.Currency != nil:
		currency, rate = *c.Currency, c.Rate
	case c.Rate.Known():
		return nil, nil, fmt.Errorf("%w: a rate without a currency", domain.ErrInvalidAccount)
	}
	// Остальное проверяет домен: пустое имя, день из будущего, курс у счета в
	// валюте книги.
	at := now()
	acc, err := domain.NewForeignAccount(name, c.Balance, currency, rate, at, now)
	if err != nil {
		return nil, nil, err
	}

	out := slices.Clone(accounts)
	if i >= 0 {
		out[i] = acc
	} else {
		out = append(out, acc)
	}
	next := maps.Clone(confs)
	if next == nil {
		next = Confirmations{}
	}
	maps.DeleteFunc(next, func(k string, _ time.Time) bool { return domain.SameAccountName(k, acc.Bank()) })
	next[acc.Bank()] = at
	return out, next, nil
}
