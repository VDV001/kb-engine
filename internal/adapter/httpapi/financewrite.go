package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ErrRepeat — такая операция в журнале уже есть, а повтор не подтвержден
// (force). Ответ 409 с найденной записью в тексте: подтверждение повтора —
// решение человека, а не ошибка записи.
var ErrRepeat = errors.New("repeat")

// TransactionAdd is one operation to record: то же, что флаги kbengine fin
// add. Сумма и дата — строки, как их набрал человек: разбирает их тот же код,
// что у команды.
type TransactionAdd struct {
	Kind, Date, Amount, Category, Subcategory, Place, Description, Source, Account, Currency string
	Force                                                                                    bool
}

// Correction is a spelling the journal changed to the one already in use.
type Correction struct{ Field, Typed, Used string }

// TransactionAdded is the recorded operation.
type TransactionAdded struct {
	ID, Date, Amount string
	Corrections      []Correction
}

// FinanceWriter records operations in the journal the server reads. Правила
// записи — у реализации (cmd: те же, что у fin add), обработчик только
// разбирает запрос и переводит ответ.
type FinanceWriter interface {
	AddTransaction(ctx context.Context, a TransactionAdd) (TransactionAdded, error)
}

// WithFinanceWriter подключает запись операций. Без нее маршрута нет.
func WithFinanceWriter(w FinanceWriter) Option {
	return func(o *options) { o.finWriter = w }
}

type transactionAddDTO struct {
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Place       string `json:"place"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Account     string `json:"account"`
	Currency    string `json:"currency"`
	Force       bool   `json:"force"`
}

type correctionDTO struct {
	Field string `json:"field"`
	Typed string `json:"typed"`
	Used  string `json:"used"`
}

type transactionAddedDTO struct {
	ID          string          `json:"id"`
	Date        string          `json:"date"`
	Amount      string          `json:"amount"`
	Corrections []correctionDTO `json:"corrections,omitempty"`
}

func handleAddTransaction(fw FinanceWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in transactionAddDTO
		if err := decodeStrict(w, r, &in); err != nil {
			writeBodyError(w, err)
			return
		}
		got, err := fw.AddTransaction(r.Context(), TransactionAdd(in))
		if err != nil {
			if errors.Is(err, ErrRepeat) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeWriteError(w, err)
			return
		}
		out := transactionAddedDTO{ID: got.ID, Date: got.Date, Amount: got.Amount}
		for _, c := range got.Corrections {
			out.Corrections = append(out.Corrections, correctionDTO(c))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(out)
	}
}
