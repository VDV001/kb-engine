package finance

import (
	"errors"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNothingToEdit сообщает, что правка не назвала ни одного поля.
var ErrNothingToEdit = errors.New("nothing to edit")

// EditParams — что именно меняется в существующей записи.
//
// Пустое поле означает «не передавали», а не «сотри»: правка суммы не должна
// молча уносить заметку и счёт. Стирание — отдельное намерение, потому и живёт
// отдельными флагами. Ровно так же разведены смыслы у `set` для каталога.
type EditParams struct {
	Date        time.Time
	Amount      domain.Money
	Category    string
	Subcategory string
	Place       string
	Description string
	Source      string
	Account     string

	ClearSubcategory bool
	ClearPlace       bool
	ClearDescription bool
	ClearAccount     bool
}

// touches отвечает, названо ли хоть одно поле.
func (p EditParams) touches() bool {
	return !p.Date.IsZero() || !p.Amount.IsZero() ||
		p.Category != "" || p.Subcategory != "" || p.Place != "" ||
		p.Description != "" || p.Source != "" || p.Account != "" ||
		p.ClearSubcategory || p.ClearPlace || p.ClearDescription || p.ClearAccount
}

// Edit возвращает запись со следующей ревизией и применёнными изменениями.
//
// Транзакция пересобирается доменом целиком, а не правится по полю: инварианты
// («у дохода нет счёта», «сумма положительна») проверяются один раз и в одном
// месте, поэтому правка не может стать дверью в обход них.
//
// Ревизия растёт всегда, когда что-то изменилось: синк отличает изменённую
// запись от нетронутой именно по ней. Правка, не назвавшая ни одного поля,
// отвергается — молча повысить ревизию значило бы соврать синку.
func Edit(rec Record, p EditParams, now func() time.Time) (Record, error) {
	if !p.touches() {
		return Record{}, ErrNothingToEdit
	}
	tx := rec.Transaction()

	params := domain.TransactionParams{
		ID:          tx.ID(),
		Kind:        tx.Kind(),
		Date:        tx.Date(),
		Amount:      tx.Amount(),
		Category:    tx.Category(),
		Subcategory: tx.Subcategory(),
		Place:       tx.Place(),
		Description: tx.Description(),
		Source:      tx.Source(),
		Account:     tx.Account(),
		Now:         now,
	}

	if !p.Date.IsZero() {
		params.Date = p.Date
	}
	if !p.Amount.IsZero() {
		params.Amount = p.Amount
	}
	if p.Category != "" {
		params.Category = p.Category
	}
	if p.Subcategory != "" {
		params.Subcategory = p.Subcategory
	}
	if p.Place != "" {
		params.Place = p.Place
	}
	if p.Description != "" {
		params.Description = p.Description
	}
	if p.Source != "" {
		params.Source = p.Source
	}
	if p.Account != "" {
		params.Account = p.Account
	}

	// Стирание идёт после присвоений: одновременно назвать и значение, и его
	// стирание — противоречие в командной строке, и выигрывает более явное.
	if p.ClearSubcategory {
		params.Subcategory = ""
	}
	if p.ClearPlace {
		params.Place = ""
	}
	if p.ClearDescription {
		params.Description = ""
	}
	if p.ClearAccount {
		params.Account = ""
	}

	edited, err := domain.NewTransaction(params)
	if err != nil {
		return Record{}, err
	}
	return NewRecord(edited, rec.Rev()+1, now())
}
