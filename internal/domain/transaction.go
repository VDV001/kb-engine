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
	Now         func() time.Time
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
	if p.Date.After(now()) {
		return Transaction{}, fmt.Errorf("%w: date %s is in the future", ErrInvalidTransaction, p.Date.Format(time.DateOnly))
	}

	category := strings.TrimSpace(p.Category)
	if p.Kind == KindExpense && category == "" {
		return Transaction{}, fmt.Errorf("%w: expense requires a category", ErrInvalidTransaction)
	}

	return Transaction{
		id:          id,
		kind:        p.Kind,
		date:        p.Date,
		amount:      p.Amount,
		category:    category,
		subcategory: strings.TrimSpace(p.Subcategory),
		place:       strings.TrimSpace(p.Place),
		description: strings.TrimSpace(p.Description),
		source:      strings.TrimSpace(p.Source),
	}, nil
}

// ID returns the stable identifier used to match rows across storage formats.
func (t Transaction) ID() string { return t.id }

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

// Source returns the origin of the record (a receipt, a transfer, a salary).
func (t Transaction) Source() string { return t.source }
