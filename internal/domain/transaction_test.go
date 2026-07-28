package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func txParams() domain.TransactionParams {
	return domain.TransactionParams{
		ID:          "01JQ0000000000000000000000",
		Kind:        "expense",
		Date:        time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(50000),
		Category:    "Еда",
		Subcategory: "Рестораны/кафе",
		Place:       "Поль Бейкери",
		Description: "Кафе",
		Source:      "Чек",
		Now:         func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) },
	}
}

func TestNewTransaction_valid(t *testing.T) {
	tx, err := domain.NewTransaction(txParams())
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if !tx.IsExpense() {
		t.Error("IsExpense() = false, want true")
	}
	if got := tx.Amount().Kopecks(); got != 50000 {
		t.Errorf("Amount = %d kopecks, want 50000", got)
	}
	// An expense must read as negative when summed into a balance, regardless of
	// how the ledger stores it — the sheet keeps expenses as positive numbers.
	if got := tx.SignedAmount().Kopecks(); got != -50000 {
		t.Errorf("SignedAmount = %d kopecks, want -50000", got)
	}
}

func TestNewTransaction_incomeIsPositive(t *testing.T) {
	p := txParams()
	p.Kind = "income"
	p.Category = ""
	p.Source = "Зарплата"
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if tx.IsExpense() {
		t.Error("IsExpense() = true, want false")
	}
	if got := tx.SignedAmount().Kopecks(); got != 50000 {
		t.Errorf("SignedAmount = %d kopecks, want 50000", got)
	}
}

func TestNewTransaction_invariants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.TransactionParams)
		want   error
	}{
		{"empty id", func(p *domain.TransactionParams) { p.ID = "" }, domain.ErrInvalidTransaction},
		{"unknown kind", func(p *domain.TransactionParams) { p.Kind = "refund" }, domain.ErrInvalidTransaction},
		{"zero date", func(p *domain.TransactionParams) { p.Date = time.Time{} }, domain.ErrInvalidTransaction},
		{"zero amount", func(p *domain.TransactionParams) { p.Amount = domain.NewMoney(0) }, domain.ErrInvalidTransaction},
		{"negative amount", func(p *domain.TransactionParams) { p.Amount = domain.NewMoney(-1) }, domain.ErrInvalidTransaction},
		{"expense without category", func(p *domain.TransactionParams) { p.Category = "" }, domain.ErrInvalidTransaction},
		{
			"date in the future",
			func(p *domain.TransactionParams) {
				p.Date = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
			},
			domain.ErrInvalidTransaction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := txParams()
			tt.mutate(&p)
			if _, err := domain.NewTransaction(p); !errors.Is(err, tt.want) {
				t.Errorf("NewTransaction() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// The clock is an input, not an ambient fact: the same ledger must validate
// identically on any machine and in any test run.
func TestNewTransaction_clockIsInjected(t *testing.T) {
	p := txParams()
	p.Date = time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if _, err := domain.NewTransaction(p); err == nil {
		t.Fatal("expected a future date to be rejected against the injected clock")
	}
	p.Now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := domain.NewTransaction(p); err != nil {
		t.Errorf("same date with a later clock should be accepted, got %v", err)
	}
}

// Categories come from a reference sheet that has already drifted from the
// data: the sheet lists 8, the ledger uses 12 (Долги, Интернет, Внешний вид,
// Банк appear only in the rows). A closed enum would reject real history, so
// the invariant is "present and normalized", not "member of a fixed set".
func TestNewTransaction_categoryIsOpenButNormalized(t *testing.T) {
	p := txParams()
	p.Category = "  Долги  "
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("category outside the reference sheet must be accepted: %v", err)
	}
	if tx.Category() != "Долги" {
		t.Errorf("Category = %q, want %q (trimmed)", tx.Category(), "Долги")
	}
}
