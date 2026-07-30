package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// An income was already refused an account, because Доходы has no column for one
// and accepting the value meant dropping it on the way to the sheet. Three other
// fields sit in exactly the same position — Доходы has no column for a category,
// a subcategory or a place either, and each one was accepted and lost.
//
// Verified before this test: an income carrying any of them comes back from
// write→read without it, and `fin add --kind income --cat Зарплата` is the
// documented way to produce one.
//
// The previous round closed one instance of this class with an invariant and left
// three open. Same argument, same place — the constructor is where a field the
// storage cannot hold has to be refused, not where it is silently dropped.
func TestNewTransaction_incomeCarriesNoExpenseOnlyField(t *testing.T) {
	base := func() domain.TransactionParams {
		return domain.TransactionParams{
			Kind:   domain.KindIncome,
			Date:   time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
			Amount: domain.NewMoney(130000),
			Source: "Зарплата",
			Now:    func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) },
		}
	}

	tests := map[string]func(*domain.TransactionParams){
		"category":    func(p *domain.TransactionParams) { p.Category = "Зарплата" },
		"subcategory": func(p *domain.TransactionParams) { p.Subcategory = "Аванс" },
		"place":       func(p *domain.TransactionParams) { p.Place = "Работа" },
	}

	for field, set := range tests {
		t.Run(field, func(t *testing.T) {
			p := base()
			p.ID = "01A"
			set(&p)

			_, err := domain.NewTransaction(p)
			if !errors.Is(err, domain.ErrIncomeFieldNotApplicable) {
				t.Fatalf("NewTransaction with a %s = %v, want ErrIncomeFieldNotApplicable", field, err)
			}
			// The message has to say which field, or the owner has to guess which of
			// the flags they passed is the problem.
			if got := err.Error(); !strings.Contains(got, field) {
				t.Errorf("error %q does not name the field at issue", got)
			}
		})
	}
}

// An income with only the fields Доходы can hold is still valid, so the
// invariant cannot be satisfied by rejecting every income.
func TestNewTransaction_incomeWithOnlyItsOwnFieldsIsValid(t *testing.T) {
	_, err := domain.NewTransaction(domain.TransactionParams{
		ID:          "01A",
		Kind:        domain.KindIncome,
		Date:        time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(130000),
		Source:      "Зарплата",
		Description: "аванс за июль",
		Now:         func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
}
