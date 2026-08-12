package finance

import (
	"errors"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNothingToEdit сообщает, что правка не назвала ни одного поля.
var ErrNothingToEdit = errors.New("nothing to edit")

// ErrNoChange сообщает, что поля названы, но запись от них не меняется.
//
// Отдельно от ErrNothingToEdit, потому что это разные ошибки человека: там
// забыли флаг, здесь передали значение, которое уже стоит. Обе одинаково
// опасны молчаливым успехом: ревизия выросла бы впустую, синк увидел бы
// запись изменённой и пошёл переписывать строку, которую переписывать нечем.
var ErrNoChange = errors.New("запись уже такая")

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
		// След осознанного повтора — решение человека о записи, а не поле,
		// которое правят: правка суммы или счёта его не отменяет. Флага для
		// него у правки намеренно нет.
		RepeatOf: tx.RepeatOf(),
		Now:      now,
	}

	params = p.applyTo(params)

	edited, err := domain.NewTransaction(params)
	if err != nil {
		return Record{}, err
	}
	// «Одинаковость» берётся у синка, а не считается заново: два определения
	// того, что запись не менялась, однажды разойдутся, и разойдутся молча.
	if Fingerprint(edited) == Fingerprint(tx) {
		return Record{}, ErrNoChange
	}
	return NewRecord(edited, rec.Rev()+1, now())
}

// applyTo накладывает названные поля на параметры транзакции.
//
// Присвоения идут до стираний: назвать одновременно и значение, и его стирание —
// противоречие в командной строке, и выигрывает более явное из двух.
func (p EditParams) applyTo(params domain.TransactionParams) domain.TransactionParams {
	if !p.Date.IsZero() {
		params.Date = p.Date
	}
	if !p.Amount.IsZero() {
		params.Amount = p.Amount
	}
	for _, f := range []struct {
		value string
		field *string
	}{
		{p.Category, &params.Category},
		{p.Subcategory, &params.Subcategory},
		{p.Place, &params.Place},
		{p.Description, &params.Description},
		{p.Source, &params.Source},
		{p.Account, &params.Account},
	} {
		if f.value != "" {
			*f.field = f.value
		}
	}
	for _, c := range []struct {
		clear bool
		field *string
	}{
		{p.ClearSubcategory, &params.Subcategory},
		{p.ClearPlace, &params.Place},
		{p.ClearDescription, &params.Description},
		{p.ClearAccount, &params.Account},
	} {
		if c.clear {
			*c.field = ""
		}
	}
	return params
}
