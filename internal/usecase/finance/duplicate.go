package finance

import (
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Duplicate returns the record a new entry would repeat, or nil when it repeats
// nothing.
//
// Written as a question asked at write time rather than a report produced later.
// A repeated expense is the failure that happens on its own — a session drops
// half-way, the person types it again, and the book carries the same purchase twice.
// Found on the dashboard a week later it is nearly unresolvable: nobody can say
// which of the two was the real purchase.
//
// Two similar expenses on one day are ordinary life, not an error: two minute
// packages on a scooter, two rides on the same fare. What tells them apart is
// the note, so a differing note means these are two purchases and the engine
// says nothing.
func Duplicate(existing []Record, p AddParams) *Record {
	// The date is resolved the way Add resolves it. Comparing the raw field
	// would switch the check off in the most common case of all — the one where
	// nobody types a date.
	want := p.Date
	if want.IsZero() {
		want = domain.Day(time.Now())
	}
	return firstMatch(existing, entry{
		kind: p.Kind, date: want, amount: p.Amount,
		category: p.Category, sub: p.Subcategory, place: p.Place,
		note: p.Description, source: p.Source, account: p.Account,
	}, "")
}

// RepeatOf asks the same question about a record that already exists: after
// this edit, does the entry repeat another one?
//
// It exists because editing went around the check entirely. Adding the same
// expense twice was refused; editing one until it matched another was not — and
// a duplicate that arrives this way is nearly unresolvable a week later, since
// nobody can say which of the two was the real purchase.
//
// Everything is compared except the record itself. Without that exception every
// edit would report the entry as a repeat of itself, and editing would stop
// working altogether.
func RepeatOf(existing []Record, edited Record) *Record {
	tx := edited.Transaction()
	return firstMatch(existing, entry{
		kind: tx.Kind(), date: tx.Date(), amount: tx.Amount(),
		category: tx.Category(), sub: tx.Subcategory(), place: tx.Place(),
		note: tx.Description(), source: tx.Source(), account: tx.Account(),
	}, tx.ID())
}

// entry — то, чем одна трата отличается от другой для человека. Одно описание
// на оба пути: две копии этого правила разошлись бы, и обход нашёлся бы сменой
// экрана — ровно так, как это уже было с записью мимо движка.
type entry struct {
	kind                                        string
	date                                        time.Time
	amount                                      domain.Money
	category, sub, place, note, source, account string
}

// firstMatch ищет запись, совпадающую с образцом. skipID исключает саму
// правимую запись; пустой id не совпадает ни с чем, поэтому путь добавления
// исключений не делает.
func firstMatch(existing []Record, want entry, skipID string) *Record {
	for i := range existing {
		tx := existing[i].Transaction()
		if skipID != "" && tx.ID() == skipID {
			continue
		}
		if want.matches(tx) {
			return &existing[i]
		}
	}
	return nil
}

// matches — совпадает ли запись с образцом. Сначала то, что различает траты
// сразу (вид, день, сумма), потом написания: перебирать шесть строк ради
// покупки другого дня незачем.
func (e entry) matches(tx domain.Transaction) bool {
	if tx.Kind() != e.kind || !tx.Date().Equal(e.date) || tx.Amount() != e.amount {
		return false
	}
	for _, pair := range [][2]string{
		{tx.Category(), e.category},
		{tx.Subcategory(), e.sub},
		{tx.Place(), e.place},
		{tx.Description(), e.note},
		{tx.Source(), e.source},
		{tx.Account(), e.account},
	} {
		if !same(pair[0], pair[1]) {
			return false
		}
	}
	return true
}

// same compares two field values the way a person would: surrounding spaces and
// case are not a difference. «Магнит» and «магнит » are one shop.
func same(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
