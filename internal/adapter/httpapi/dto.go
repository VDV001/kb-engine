package httpapi

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
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

// accountDTO is one balance from the workbook's «Счета» sheet.
type accountDTO struct {
	Bank    string `json:"bank"`
	Balance string `json:"balance"`
	Updated string `json:"updated"`
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

func toAccountDTO(a domain.Account) accountDTO {
	return accountDTO{
		Bank:    a.Bank(),
		Balance: a.Balance().String(),
		Updated: a.Updated().Format(time.DateOnly),
	}
}

// entryDTO is the JSON shape of an entry exposed by the API. Optional aspects
// are omitted when absent.
type entryDTO struct {
	ID           int      `json:"id"`
	HabrID       *int     `json:"habr_id,omitempty"`
	Title        string   `json:"title"`
	URL          string   `json:"url,omitempty"`
	Category     string   `json:"category"`
	Kind         string   `json:"kind"`
	Lifecycle    string   `json:"lifecycle"`
	Verdict      *string  `json:"verdict,omitempty"`
	ReadState    *string  `json:"read_state,omitempty"`
	PublishStage *string  `json:"publish_stage,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	DateAdded    string   `json:"date_added,omitempty"`
	Description  string   `json:"description,omitempty"`
	Author       string   `json:"author,omitempty"`
	Source       string   `json:"source,omitempty"`
}

func toDTO(e domain.Entry) entryDTO {
	d := entryDTO{
		ID:          e.ID(),
		HabrID:      e.HabrID(),
		Title:       e.Title(),
		URL:         e.URL(),
		Category:    e.Category().String(),
		Kind:        e.Kind(),
		Lifecycle:   e.Lifecycle().String(),
		Tags:        e.Tags(),
		Description: e.Description(),
		Author:      e.Author(),
		Source:      e.Source(),
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
	if t := e.DateAdded(); t != nil {
		d.DateAdded = t.Format(time.DateOnly)
	}
	return d
}
