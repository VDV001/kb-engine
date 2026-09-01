package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ErrInvalidAccount is returned when account parameters violate an invariant.
var ErrInvalidAccount = errors.New("invalid account")

// Account is a bank balance snapshot as recorded in the ledger.
//
// A balance may legitimately be negative (an overdraft or a credit card), so
// unlike a transaction amount its sign is not constrained.
type Account struct {
	bank     string
	balance  Money
	updated  time.Time
	currency Currency
	rate     Rate
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
	// День сравнивается с днём, как в NewTransaction. Голое updated.After(now())
	// сравнивало бы ДЕНЬ из ячейки (excelize отдаёт его полуночью UTC) с
	// МОМЕНТОМ часов: между полуночью и пятью утра по книге сегодняшнее число
	// оказывалось «в будущем», и домен отвергал счёт, записанный самим движком.
	if !updated.IsZero() && Day(updated).After(Day(now())) {
		return Account{}, fmt.Errorf("%w: updated %s is in the future", ErrInvalidAccount, updated.Format(time.DateOnly))
	}
	return Account{bank: name, balance: balance, updated: updated}, nil
}

// NewForeignAccount validates and returns an account held in another currency.
//
// Отдельный конструктор, а не два новых аргумента у NewAccount, — решение по
// #332. Лист «Счета» существует в единственном экземпляре и колонок валюты не
// имеет; расширив прежнюю сигнатуру, мы бы заставили каждое существующее место
// сказать «рубль», и обратная совместимость держалась бы на том, что никто из
// них не забыл это сделать.
//
// Баланс здесь — сумма В СВОЕЙ валюте, а не пересчёт: сохранив только рублёвую
// оценку, мы потеряли бы ровно то, ради чего валюта заводится. Курс хранится
// рядом и может быть неизвестен.
func NewForeignAccount(bank string, balance Money, currency Currency, rate Rate, updated time.Time, now func() time.Time) (Account, error) {
	acc, err := NewAccount(bank, balance, updated, now)
	if err != nil {
		return Account{}, err
	}
	// Курс у базовой валюты — противоречие, а не безобидная избыточность: он
	// означал бы, что рубль оценивают в рублях по какому-то отличному от
	// единицы числу, и итог по счетам стал бы зависеть от того, кто его вписал.
	if currency.IsBase() && rate.Known() {
		return Account{}, fmt.Errorf("%w: %s is the base currency and cannot carry a rate", ErrInvalidAccount, currency)
	}
	acc.currency = currency
	acc.rate = rate
	return acc, nil
}

// Currency returns the unit the balance is held in; the zero value is the base
// currency, so an account read from a book without the column stays as it was.
func (a Account) Currency() Currency { return a.currency }

// Rate returns the rate the balance was valued at, which may be unknown.
func (a Account) Rate() Rate { return a.rate }

// BaseValue returns the balance in the currency the book is kept in, and
// whether it is known at all.
//
// Второе значение — не вежливость, а весь смысл: у наличной валюты курса может
// не быть вовсе, и витрина обязана сказать «оценка неизвестна», а не показать
// ноль. Ноль здесь читался бы как «денег нет».
func (a Account) BaseValue() (Money, bool) {
	if a.currency.IsBase() {
		return a.balance, true
	}
	return a.rate.Apply(a.balance)
}

// Bank returns the account name.
func (a Account) Bank() string { return a.bank }

// Balance returns the recorded balance.
func (a Account) Balance() Money { return a.balance }

// Updated returns when the balance was last confirmed; zero when unknown.
func (a Account) Updated() time.Time { return a.updated }

// groupSeparator is the arrow the book already uses to say that an account is
// one of several of a kind: «Резерв → Наличные», «Займ → Коллеге».
//
// The spaces around it are typography rather than meaning, so the split ignores
// them — otherwise «Займ→Коллеге» would be a group of its own that only looks the
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
//
// Every kind of space goes, not just the ASCII one. A name copied out of a bank
// app carries a non-breaking space, and «Т\u00a0Банк» has to be the same account
// as «Т-Банк» — otherwise `fin balance --create` opens a second row for an
// account the sheet already holds.
//
// Dropping spaces by category also makes the fold idempotent, which the earlier
// version was not: removing a hyphen could expose a whitespace character that
// TrimSpace had already walked past, so a name had no single canonical form.
// A canonicaliser that is not idempotent does not define a canonical form.
func FoldName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
