package finance

import (
	"cmp"
	"maps"
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

// SubcategoryTotal is what one subcategory cost inside its category.
//
// Category and Subcategory stay separate fields on purpose. The dashboard shows
// them joined as «Категория → Подкатегория», but that arrow is a label: joining
// here would put an interface string in a usecase, and would also make the row
// unusable for anything that wants to group by category again.
type SubcategoryTotal struct {
	Category    string
	Subcategory string
	Total       domain.Money
	Count       int
}

// MonthTotal is what one calendar month cost. Month is YYYY-MM.
//
// The key carries the year. The Python dashboard indexed a 12-slot array by
// month number alone, so with four years of history every January landed in the
// same bar — a chart that silently mixes years.
type MonthTotal struct {
	Month string
	Total domain.Money
	Count int
}

// DayTotal is what one calendar day cost. Date is YYYY-MM-DD.
type DayTotal struct {
	Date  string
	Total domain.Money
	Count int
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

	// The cuts below drive the dashboard's finance charts. Like ByCategory they
	// cover expenses only, except IncomeBySource; a row is left out when the
	// field is empty, because a missing subcategory is missing data rather than
	// a group called "other".
	BySubcategory []SubcategoryTotal
	// ByPlace sums across categories: one shop visited for food and for fun is
	// still one place.
	ByPlace []CategoryTotal
	// BySource is what the money was paid with. IncomeBySource is where income
	// came from. Both read the same field on the transaction, which is exactly
	// why they are two lists and not one.
	BySource       []CategoryTotal
	IncomeBySource []CategoryTotal
	// ByMonth and ByDay are chronological, oldest first, and contain only the
	// periods that actually have expenses — filling the gaps is the chart's job,
	// and it needs a window (the density chart shows 31 days) that a report has
	// no business guessing.
	ByMonth []MonthTotal
	ByDay   []DayTotal
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
	byPlace := make(map[string]CategoryTotal)
	bySource := make(map[string]CategoryTotal)
	incomeBySource := make(map[string]CategoryTotal)
	bySubcategory := make(map[[2]string]SubcategoryTotal)
	byMonth := make(map[string]MonthTotal)
	byDay := make(map[string]DayTotal)

	for _, r := range recs {
		tx := r.Transaction()
		s.Net = s.Net.Add(tx.SignedAmount())
		if !tx.IsExpense() {
			s.IncomeCount++
			s.Income = s.Income.Add(tx.Amount())
			if src := tx.Source(); src != "" {
				incomeBySource[src] = addTo(incomeBySource[src], src, tx.Amount())
			}
			continue
		}
		s.ExpenseCount++
		s.Expenses = s.Expenses.Add(tx.Amount())

		byCategory[tx.Category()] = addTo(byCategory[tx.Category()], tx.Category(), tx.Amount())
		if a := tx.Account(); a != "" {
			byAccount[a] = addTo(byAccount[a], a, tx.Amount())
		}
		if p := tx.Place(); p != "" {
			byPlace[p] = addTo(byPlace[p], p, tx.Amount())
		}
		if src := tx.Source(); src != "" {
			bySource[src] = addTo(bySource[src], src, tx.Amount())
		}
		if sub := tx.Subcategory(); sub != "" {
			k := [2]string{tx.Category(), sub}
			e := bySubcategory[k]
			e.Category, e.Subcategory = k[0], k[1]
			e.Total = e.Total.Add(tx.Amount())
			e.Count++
			bySubcategory[k] = e
		}

		month := tx.Date().Format("2006-01")
		m := byMonth[month]
		m.Month = month
		m.Total = m.Total.Add(tx.Amount())
		m.Count++
		byMonth[month] = m

		day := tx.Date().Format(time.DateOnly)
		d := byDay[day]
		d.Date = day
		d.Total = d.Total.Add(tx.Amount())
		d.Count++
		byDay[day] = d
	}

	s.ByCategory = ranked(byCategory)
	s.ByAccount = ranked(byAccount)
	s.ByPlace = ranked(byPlace)
	s.BySource = ranked(bySource)
	s.IncomeBySource = ranked(incomeBySource)
	s.BySubcategory = rankedSubcategories(bySubcategory)

	// YYYY-MM and YYYY-MM-DD sort lexicographically in calendar order, so
	// sorting the keys is the same as sorting by date, without parsing them back.
	for _, k := range slices.Sorted(maps.Keys(byMonth)) {
		s.ByMonth = append(s.ByMonth, byMonth[k])
	}
	for _, k := range slices.Sorted(maps.Keys(byDay)) {
		s.ByDay = append(s.ByDay, byDay[k])
	}
	return s
}

// rankedSubcategories orders like ranked, with the category and then the
// subcategory breaking ties so the order is the same on every run.
func rankedSubcategories(m map[[2]string]SubcategoryTotal) []SubcategoryTotal {
	out := make([]SubcategoryTotal, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b SubcategoryTotal) int {
		if c := cmp.Compare(b.Total.Kopecks(), a.Total.Kopecks()); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Category, b.Category); c != 0 {
			return c
		}
		return cmp.Compare(a.Subcategory, b.Subcategory)
	})
	return out
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
