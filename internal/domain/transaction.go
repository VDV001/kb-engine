package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidTransaction is returned when transaction parameters violate an
// invariant.
var ErrInvalidTransaction = errors.New("invalid transaction")

// ErrAccountNotApplicable is returned when an income is given an account.
//
// Доходы records where money came from — a salary, a transfer — and has no
// column for which account it landed in. Accepting the value and dropping it on
// the way to the sheet is what made a written transaction come back different
// from itself.
var ErrAccountNotApplicable = errors.New("an income does not carry an account")

// ErrIncomeFieldNotApplicable is returned when an income is given a field only
// an expense has.
//
// Доходы has columns for the day, the source, the amount and a description, and
// none for a category, a subcategory or a place. Same reasoning as the account
// above: a value this side accepts and the sheet cannot hold comes back missing,
// so it is refused where it is offered rather than dropped in transit.
var ErrIncomeFieldNotApplicable = errors.New("an income does not carry this field")

// Transaction kinds. The sheet stores expenses as positive amounts and flips
// the sign only when summing (see SignedAmount); a negative expense is a
// refund and therefore adds to the balance.
const (
	KindExpense = "expense"
	KindIncome  = "income"
)

var canonicalTxKinds = map[string]struct{}{
	KindExpense: {},
	KindIncome:  {},
}

// Transaction is a single ledger entry. Constructed only through
// NewTransaction so the invariants below cannot be bypassed.
type Transaction struct {
	id          string
	kind        string
	date        time.Time
	amount      Money
	category    string
	subcategory string
	place       string
	description string
	source      string
	account     string
}

// TransactionParams carries the raw values for NewTransaction.
//
// Now supplies the clock. It is a parameter rather than a call to time.Now
// inside the domain: validation that depends on ambient time gives different
// answers on different machines, and an import run twice would not agree with
// itself.
type TransactionParams struct {
	ID          string
	Kind        string
	Date        time.Time
	Amount      Money
	Category    string
	Subcategory string
	Place       string
	Description string
	Source      string
	Account     string
	Now         func() time.Time
}

// Day reduces an instant to the calendar day it falls on, in its own location,
// expressed as midnight UTC.
//
// The ledger records days, not moments, so "in the future" has to be a question
// about days. Comparing a day against an instant mixes units, and the mistake
// is invisible at UTC: at 02:41 in Yekaterinburg it is already the 29th while
// UTC still reads the 28th, and an entry made then is not a future entry.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// NewTransaction validates the parameters and returns a Transaction.
func NewTransaction(p TransactionParams) (Transaction, error) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return Transaction{}, fmt.Errorf("%w: id is required", ErrInvalidTransaction)
	}
	if _, ok := canonicalTxKinds[p.Kind]; !ok {
		return Transaction{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidTransaction, p.Kind)
	}
	if p.Date.IsZero() {
		return Transaction{}, fmt.Errorf("%w: date is required", ErrInvalidTransaction)
	}
	if p.Amount.IsZero() {
		// Zero is the one amount that carries no information — a blank row, not a
		// transaction. Negative amounts are legitimate: the ledger records refunds
		// as negative expenses ("Продажа игры (возврат)"), and SignedAmount turns
		// those back into money returning to the balance.
		return Transaction{}, fmt.Errorf("%w: amount must not be zero", ErrInvalidTransaction)
	}

	now := p.Now
	if now == nil {
		return Transaction{}, fmt.Errorf("%w: clock is required", ErrInvalidTransaction)
	}
	if Day(p.Date).After(Day(now())) {
		return Transaction{}, fmt.Errorf("%w: date %s is in the future", ErrInvalidTransaction, p.Date.Format(time.DateOnly))
	}

	category := strings.TrimSpace(p.Category)
	if p.Kind == KindExpense && category == "" {
		return Transaction{}, fmt.Errorf("%w: expense requires a category", ErrInvalidTransaction)
	}

	account := strings.TrimSpace(p.Account)
	if p.Kind == KindIncome && account != "" {
		return Transaction{}, fmt.Errorf("%w: %w (got %q)", ErrInvalidTransaction, ErrAccountNotApplicable, account)
	}

	subcategory, place := strings.TrimSpace(p.Subcategory), strings.TrimSpace(p.Place)
	if p.Kind == KindIncome {
		// Named one by one rather than reported as "an expense field", because the
		// owner passed a specific flag and has to know which one to drop.
		for _, f := range []struct{ name, value string }{
			{"category", category},
			{"subcategory", subcategory},
			{"place", place},
		} {
			if f.value != "" {
				return Transaction{}, fmt.Errorf("%w: %w: %s (got %q)",
					ErrInvalidTransaction, ErrIncomeFieldNotApplicable, f.name, f.value)
			}
		}
	}

	return Transaction{
		id:          id,
		kind:        p.Kind,
		date:        p.Date,
		amount:      p.Amount,
		category:    category,
		subcategory: subcategory,
		place:       place,
		description: strings.TrimSpace(p.Description),
		source:      strings.TrimSpace(p.Source),
		account:     account,
	}, nil
}

// ID returns the stable identifier used to match rows across storage formats.
func (t Transaction) ID() string { return t.id }

// WithID returns a copy carrying a different identity, leaving every other
// field alone. Used on first import, where a row read out of the spreadsheet
// arrives with a positional id and is given a stable one.
//
// The receiver is a value on purpose: the original keeps its own id, so a
// caller cannot accidentally end up with two records sharing an identity.
func (t Transaction) WithID(id string) (Transaction, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Transaction{}, fmt.Errorf("%w: id is required", ErrInvalidTransaction)
	}
	t.id = id
	return t, nil
}

// Kind reports whether this is an expense or an income.
func (t Transaction) Kind() string { return t.kind }

// IsExpense reports whether the transaction reduces the balance.
func (t Transaction) IsExpense() bool { return t.kind == KindExpense }

// Date returns the date the transaction happened.
func (t Transaction) Date() time.Time { return t.date }

// Amount returns the amount as recorded, before the kind's sign is applied.
// Negative for a refund.
func (t Transaction) Amount() Money { return t.amount }

// SignedAmount returns the amount as it contributes to a balance: negative for
// an expense, positive for an income. Summing these gives the net directly.
func (t Transaction) SignedAmount() Money {
	if t.IsExpense() {
		return NewMoney(-t.amount.Kopecks())
	}
	return t.amount
}

// Category returns the trimmed category. Open by design: the reference sheet
// has drifted from the data, so membership is not enforced here.
func (t Transaction) Category() string { return t.category }

// Subcategory returns the trimmed subcategory, empty when absent.
func (t Transaction) Subcategory() string { return t.subcategory }

// Place returns where the money was spent, empty when absent.
func (t Transaction) Place() string { return t.place }

// Description returns the free-form note, empty when absent.
func (t Transaction) Description() string { return t.description }

// Source returns how the record came to be — a receipt, or entered by hand.
func (t Transaction) Source() string { return t.source }

// Account returns which account the money moved through, empty when the row
// does not say. Open by design: the workbook lists five, and a closed set would
// reject the sixth on the day one is opened.
func (t Transaction) Account() string { return t.account }
