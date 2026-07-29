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
	Account  string
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
	// Typed by hand into a spreadsheet; case is not a distinction anyone making
	// the entry intended to draw.
	if f.Account != "" && !strings.EqualFold(tx.Account(), f.Account) {
		return false
	}
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
	// ByAccount is the same breakdown by the account the money left from. Rows
	// that name no account are left out rather than lumped into a blank one.
	ByAccount []CategoryTotal
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
	byAccount := make(map[string]CategoryTotal)

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

		byCategory[tx.Category()] = addTo(byCategory[tx.Category()], tx.Category(), tx.Amount())
		if a := tx.Account(); a != "" {
			byAccount[a] = addTo(byAccount[a], a, tx.Amount())
		}
	}

	s.ByCategory = ranked(byCategory)
	s.ByAccount = ranked(byAccount)
	return s
}

func addTo(t CategoryTotal, name string, amount domain.Money) CategoryTotal {
	t.Category = name
	t.Total = t.Total.Add(amount)
	t.Count++
	return t
}

// ranked flattens a breakdown, biggest first, with the name breaking ties so
// the order is the same on every run.
func ranked(m map[string]CategoryTotal) []CategoryTotal {
	out := make([]CategoryTotal, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b CategoryTotal) int {
		if c := cmp.Compare(b.Total.Kopecks(), a.Total.Kopecks()); c != 0 {
			return c
		}
		return cmp.Compare(a.Category, b.Category)
	})
	return out
}
