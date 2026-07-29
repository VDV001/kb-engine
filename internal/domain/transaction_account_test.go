package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Доходы has four columns and none of them is an account, so an income with one
// is not a row the ledger can hold. Every layer used to agree to it and then
// drop it: the CLI accepted --account, the domain allowed it, the write ignored
// it, and Fingerprint counted it — so the value came back different from what
// went in and the round trip erased it.
//
// The rule is about what an income is, not about how a spreadsheet is shaped,
// so it lives here rather than at the boundary that first sees it.
func TestNewTransaction_rejectsAnAccountOnIncome(t *testing.T) {
	p := domain.TransactionParams{
		ID:      "01JQ0000000000000000000000",
		Kind:    domain.KindIncome,
		Date:    time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:  domain.NewMoney(9000000),
		Source:  "Зарплата",
		Account: "Сбербанк",
		Now:     func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) },
	}

	_, err := domain.NewTransaction(p)
	if !errors.Is(err, domain.ErrAccountNotApplicable) {
		t.Fatalf("NewTransaction error = %v, want ErrAccountNotApplicable", err)
	}
	// Callers that already branch on the general invariant error must keep
	// working; this is a kind of invalid transaction, not a separate category.
	if !errors.Is(err, domain.ErrInvalidTransaction) {
		t.Errorf("error %v does not match ErrInvalidTransaction", err)
	}
}

// Whitespace is not an account. Trimming happens after validation elsewhere in
// this constructor, and a rule that a space slips past is not a rule.
func TestNewTransaction_treatsABlankAccountOnIncomeAsAbsent(t *testing.T) {
	p := domain.TransactionParams{
		ID:      "01JQ0000000000000000000000",
		Kind:    domain.KindIncome,
		Date:    time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:  domain.NewMoney(9000000),
		Source:  "Зарплата",
		Account: "   ",
		Now:     func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) },
	}

	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if got := tx.Account(); got != "" {
		t.Errorf("Account() = %q, want empty", got)
	}
}

// The rule is about income alone: Расходы does carry an account, on 454 of the
// owner's 507 rows.
func TestNewTransaction_keepsAnAccountOnAnExpense(t *testing.T) {
	p := txParams()
	p.Account = "Сбербанк"

	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if got := tx.Account(); got != "Сбербанк" {
		t.Errorf("Account() = %q, want Сбербанк", got)
	}
}
