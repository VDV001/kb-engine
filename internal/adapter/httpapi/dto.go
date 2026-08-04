package httpapi

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// transactionDTO is one ledger row on the wire.
//
// Amount is a decimal string, not a number: the ledger keeps kopecks as int64
// precisely so that 89.99 does not become 89.98999999999999, and putting it
// through a JSON float would undo that on the way to the screen.
type transactionDTO struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	Place       string `json:"place,omitempty"`
	Description string `json:"description,omitempty"`
	Account     string `json:"account,omitempty"`
	Source      string `json:"source,omitempty"`
}

// namedTotalDTO is one row of a breakdown: a name, what it adds up to, and how
// many rows went into it. Used for every cut whose key is a single string —
// category, place, payment source, income source.
type namedTotalDTO struct {
	Name  string `json:"name"`
	Total string `json:"total"`
	Count int    `json:"count"`
}

// subcategoryTotalDTO keeps the category and the subcategory apart. The
// dashboard shows them joined as «Категория → Подкатегория», but that arrow is
// a label and belongs to the view, not to the wire.
type subcategoryTotalDTO struct {
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Total       string `json:"total"`
	Count       int    `json:"count"`
}

// periodTotalDTO is one calendar period: YYYY-MM for months, YYYY-MM-DD for
// days. Only periods that actually have expenses appear — filling the gaps needs
// a window, and the window belongs to whichever chart is drawing it.
type periodTotalDTO struct {
	Period string `json:"period"`
	Total  string `json:"total"`
	Count  int    `json:"count"`
}

// financeSummaryDTO is the finance view's arithmetic, done once, on the server.
//
// Every list is present even when empty: a missing field is indistinguishable
// from "no data" in the client, and the chart would quietly disappear instead of
// rendering an empty state.
type financeSummaryDTO struct {
	ExpenseCount   int                   `json:"expenseCount"`
	Expenses       string                `json:"expenses"`
	IncomeCount    int                   `json:"incomeCount"`
	Income         string                `json:"income"`
	Net            string                `json:"net"`
	ByCategory     []namedTotalDTO       `json:"byCategory"`
	ByAccount      []namedTotalDTO       `json:"byAccount"`
	ByPlace        []namedTotalDTO       `json:"byPlace"`
	BySource       []namedTotalDTO       `json:"bySource"`
	IncomeBySource []namedTotalDTO       `json:"incomeBySource"`
	BySubcategory  []subcategoryTotalDTO `json:"bySubcategory"`
	ByMonth        []periodTotalDTO      `json:"byMonth"`
	ByDay          []periodTotalDTO      `json:"byDay"`
}

func toNamedTotals(in []finance.CategoryTotal) []namedTotalDTO {
	out := make([]namedTotalDTO, 0, len(in))
	for _, c := range in {
		out = append(out, namedTotalDTO{Name: c.Category, Total: c.Total.String(), Count: c.Count})
	}
	return out
}

func toFinanceSummaryDTO(s finance.Summary) financeSummaryDTO {
	dto := financeSummaryDTO{
		ExpenseCount:   s.ExpenseCount,
		Expenses:       s.Expenses.String(),
		IncomeCount:    s.IncomeCount,
		Income:         s.Income.String(),
		Net:            s.Net.String(),
		ByCategory:     toNamedTotals(s.ByCategory),
		ByAccount:      toNamedTotals(s.ByAccount),
		ByPlace:        toNamedTotals(s.ByPlace),
		BySource:       toNamedTotals(s.BySource),
		IncomeBySource: toNamedTotals(s.IncomeBySource),
		BySubcategory:  make([]subcategoryTotalDTO, 0, len(s.BySubcategory)),
		ByMonth:        make([]periodTotalDTO, 0, len(s.ByMonth)),
		ByDay:          make([]periodTotalDTO, 0, len(s.ByDay)),
	}
	for _, c := range s.BySubcategory {
		dto.BySubcategory = append(dto.BySubcategory, subcategoryTotalDTO{
			Category: c.Category, Subcategory: c.Subcategory,
			Total: c.Total.String(), Count: c.Count,
		})
	}
	for _, m := range s.ByMonth {
		dto.ByMonth = append(dto.ByMonth, periodTotalDTO{Period: m.Month, Total: m.Total.String(), Count: m.Count})
	}
	for _, d := range s.ByDay {
		dto.ByDay = append(dto.ByDay, periodTotalDTO{Period: d.Date, Total: d.Total.String(), Count: d.Count})
	}
	return dto
}

// accountDTO is one balance from the workbook's «Счета» sheet.
type accountDTO struct {
	Bank    string `json:"bank"`
	Balance string `json:"balance"`
	Updated string `json:"updated"`
	// Current — остаток на сейчас: подтверждённое число минус траты, записанные
	// после подтверждения. Spent — сколько именно ушло. Оба считает usecase, а
	// не страница: иначе веб и терминал разошлись бы в арифметике молча.
	Current           string `json:"current"`
	Spent             string `json:"spent"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
}

func toTransactionDTO(t domain.Transaction) transactionDTO {
	return transactionDTO{
		ID:          t.ID(),
		Kind:        t.Kind(),
		Date:        t.Date().Format(time.DateOnly),
		Amount:      t.Amount().String(),
		Category:    t.Category(),
		Subcategory: t.Subcategory(),
		Place:       t.Place(),
		Description: t.Description(),
		Account:     t.Account(),
		Source:      t.Source(),
	}
}

// toBalanceDTO собирает счёт вместе с расчётом остатка.
func toBalanceDTO(b finance.AccountBalance) accountDTO {
	return accountDTO{
		Bank:              b.Bank,
		Balance:           b.Confirmed.String(),
		Updated:           b.ConfirmedOn,
		Current:           b.Current.String(),
		Spent:             b.Spent.String(),
		NeedsConfirmation: b.NeedsConfirmation,
	}
}

// entryDTO is the JSON shape of an entry exposed by the API. Optional aspects
// are omitted when absent.
type entryDTO struct {
	ID            int      `json:"id"`
	HabrID        *int     `json:"habr_id,omitempty"`
	Title         string   `json:"title"`
	URL           string   `json:"url,omitempty"`
	Category      string   `json:"category"`
	Kind          string   `json:"kind"`
	Lifecycle     string   `json:"lifecycle"`
	Verdict       *string  `json:"verdict,omitempty"`
	ReadState     *string  `json:"read_state,omitempty"`
	PublishStage  *string  `json:"publish_stage,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DateAdded     string   `json:"date_added,omitempty"`
	DateCreated   string   `json:"date_created,omitempty"`
	Description   string   `json:"description,omitempty"`
	Author        string   `json:"author,omitempty"`
	Source        string   `json:"source,omitempty"`
	IsTranslation bool     `json:"is_translation,omitempty"`
	// Путь к собственному тексту записи и ссылки на связанные записи. После
	// ADR-0004 вдвоём они и есть связь «статья → её разбор»: файл принадлежит
	// той записи, чей он, а цитирующие статьи держат related_ids. Оба поля
	// опускаются пустыми — «разбора нет» и «разбор по пустому пути» на экране
	// выглядели бы одинаково.
	// Дата выхода у автора и дата глубокого разбора. Обе опускаются пустыми:
	// «даты нет» и «дата пустая» на экране выглядели бы одинаково.
	HabrDate     string `json:"habr_date,omitempty"`
	DeepReadDate string `json:"deep_read_date,omitempty"`
	File         string `json:"file,omitempty"`
	RelatedIDs   []int  `json:"related_ids,omitempty"`
}

func toDTO(e domain.Entry) entryDTO {
	d := entryDTO{
		ID:            e.ID(),
		HabrID:        e.HabrID(),
		Title:         e.Title(),
		URL:           e.URL(),
		Category:      e.Category().String(),
		Kind:          e.Kind(),
		Lifecycle:     e.Lifecycle().String(),
		Tags:          e.Tags(),
		Description:   e.Description(),
		Author:        e.Author(),
		Source:        e.Source(),
		IsTranslation: e.IsTranslation(),
		File:          e.NotesFile(),
		RelatedIDs:    e.RelatedIDs(),
	}
	if v := e.Verdict(); v != nil {
		s := v.String()
		d.Verdict = &s
	}
	if r := e.ReadState(); r != nil {
		s := r.String()
		d.ReadState = &s
	}
	if p := e.PublishStage(); p != nil {
		s := p.String()
		d.PublishStage = &s
	}
	// Оба поля, а не одно: они почти не пересекаются, и запись обычно несёт
	// ровно одно из двух. Какое показывать — решает вид, но выбирать он может
	// только из того, что доехало.
	if t := e.DateAdded(); t != nil {
		d.DateAdded = t.Format(time.DateOnly)
	}
	if t := e.DateCreated(); t != nil {
		d.DateCreated = t.Format(time.DateOnly)
	}
	if t := e.HabrDate(); t != nil {
		d.HabrDate = t.Format(time.DateOnly)
	}
	if t := e.DeepReadDate(); t != nil {
		d.DeepReadDate = t.Format(time.DateOnly)
	}
	return d
}
