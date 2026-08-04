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

// groupSeparator is the arrow the book already uses to say that an account is
// one of several of a kind: «Заморозка → Хранение», «Долг → Отец».
//
// The spaces around it are typography rather than meaning, so the split ignores
// them — otherwise «Долг→Отец» would be a group of its own that only looks the
// same on screen.
const groupSeparator = "→"

// Group returns the kind an account belongs to, or "" for an ordinary account.
//
// Витрины спрашивают об этом потому, что деньги на карте, деньги на заморозке и
// деньги, которых у тебя сейчас нет, потому что их занял человек, — это не одна
// сумма. Итог, который складывает их молча, отвечает не на тот вопрос, который
// задают, глядя на него.
func (a Account) Group() string {
	group, _ := SplitAccountName(a.bank)
	return group
}

// NameWithinGroup returns what the account is called inside its group, or the
// whole name when it has none. Only the first arrow splits: a second one is
// part of the name, not a third level.
func (a Account) NameWithinGroup() string {
	_, rest := SplitAccountName(a.bank)
	return rest
}

// SplitAccountName splits an account name into its group and what it is called
// inside that group. A name without an arrow has no group and stays whole.
//
// Callers that hold a name rather than an Account use this directly — a screen
// that re-derives the group from the string itself is a second copy of the rule,
// and the copy is the one that starts splitting differently.
func SplitAccountName(bank string) (group, name string) {
	before, after, found := strings.Cut(bank, groupSeparator)
	if !found {
		return "", strings.TrimSpace(bank)
	}
	return strings.TrimSpace(before), strings.TrimSpace(after)
}

// SameAccountName reports whether two spellings name the same account.
//
// Case, «ё» and hyphens carry no meaning in these names, and neither do spaces:
// «Т-Банк», «т банк» and «тбанк» are one account. The rule belongs to the
// domain because more than one surface asks it — the quick entry line in the
// terminal matches a typed word against the vocabulary, and the Счета sheet has
// to refuse a new row for an account it already holds under another spelling.
//
// Two blank names are not the same account but no account at all, so the
// comparison refuses them rather than reporting a match.
func SameAccountName(a, b string) bool {
	folded := FoldName(a)
	return folded != "" && folded == FoldName(b)
}

// FoldName reduces a name to what its spellings have in common.
func FoldName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}
