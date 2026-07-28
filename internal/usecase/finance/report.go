package finance

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Filter narrows a ledger. A zero field means "any", so the zero Filter matches
// everything — the common case of `fin list` with no flags.
type Filter struct {
	Year     int
	Month    time.Month
	Category string
	Kind     string
}

// Match returns the records the filter accepts, in the order given.
func Match(recs []Record, f Filter) []Record {
	var out []Record
	for _, r := range recs {
		if f.accepts(r) {
			out = append(out, r)
		}
	}
	return out
}

func (f Filter) accepts(r Record) bool {
	tx := r.Transaction()
	if f.Year != 0 && tx.Date().Year() != f.Year {
		return false
	}
	if f.Month != 0 && tx.Date().Month() != f.Month {
		return false
	}
	if f.Kind != "" && tx.Kind() != f.Kind {
		return false
	}
	// Categories are typed by hand into a spreadsheet; case is not a distinction
	// anyone making the entry intended to draw.
	return f.Category == "" || strings.EqualFold(tx.Category(), f.Category)
}

// CategoryTotal is what one category cost over the records summarized.
type CategoryTotal struct {
	Category string
	Total    domain.Money
	Count    int
}

// Summary is the shape of a report over a set of records.
type Summary struct {
	ExpenseCount int
	Expenses     domain.Money
	IncomeCount  int
	Income       domain.Money
	Net          domain.Money
	// ByCategory covers expenses only, biggest first — the point of the
	// breakdown is what to look at.
	ByCategory []CategoryTotal
}

// Summarize totals the records.
//
// Expenses are summed as recorded, which means a refund (a negative expense)
// comes off the total instead of adding to it. Net is the sum of signed
// amounts, so it is the same arithmetic the balance saw and cannot drift away
// from the other two numbers.
func Summarize(recs []Record) Summary {
	var s Summary
	byCategory := make(map[string]CategoryTotal)

	for _, r := range recs {
		tx := r.Transaction()
		s.Net = s.Net.Add(tx.SignedAmount())
		if !tx.IsExpense() {
			s.IncomeCount++
			s.Income = s.Income.Add(tx.Amount())
			continue
		}
		s.ExpenseCount++
		s.Expenses = s.Expenses.Add(tx.Amount())

		c := byCategory[tx.Category()]
		c.Category = tx.Category()
		c.Total = c.Total.Add(tx.Amount())
		c.Count++
		byCategory[tx.Category()] = c
	}

	s.ByCategory = make([]CategoryTotal, 0, len(byCategory))
	for _, c := range byCategory {
		s.ByCategory = append(s.ByCategory, c)
	}
	slices.SortFunc(s.ByCategory, func(a, b CategoryTotal) int {
		// Descending by amount, then by name so the order is stable across runs.
		if c := cmp.Compare(b.Total.Kopecks(), a.Total.Kopecks()); c != 0 {
			return c
		}
		return cmp.Compare(a.Category, b.Category)
	})
	return s
}
