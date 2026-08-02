package domain

import "strings"

// Как записывается перемещение денег между своими счетами: расход помечается
// категорией и подкатегорией, доход — источником.
//
// Значения взяты из живой книги, а не назначены: так владелец писал их четыре
// года, и переименовать их здесь значило бы объявить прошлые записи ошибочными.
const (
	transferCategory    = "Прочее"
	transferSubcategory = "Переводы"
	transferSource      = "Перевод себе"
)

// IsInternalTransfer reports whether this row moves money between the owner's
// own accounts instead of into or out of the household.
//
// Such a row is neither an expense nor an income: the money did not arrive and
// did not leave, it was moved. Adding it to real spending says the person spent
// more than they did.
//
// The rule lives here rather than in whatever assembles a report, because both
// surfaces have to answer the same question the same way. It used to live in
// the dashboard's build script and nowhere else, and the two totals disagreed
// for months with no way to tell which was right.
func (t Transaction) IsInternalTransfer() bool {
	if t.IsExpense() {
		return eqField(t.category, transferCategory) && eqField(t.subcategory, transferSubcategory)
	}
	return eqField(t.source, transferSource)
}

// eqField compares two field values the way a person reading them would:
// surrounding spaces and case carry no meaning.
func eqField(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
