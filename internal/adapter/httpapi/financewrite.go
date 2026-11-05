package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrRepeat — такая операция в журнале уже есть, а повтор не подтвержден
// (force). Ответ 409 с найденной записью в тексте: подтверждение повтора —
// решение человека, а не ошибка записи.
var ErrRepeat = errors.New("repeat")

// TransactionAdd is one operation to record: то же, что флаги kbengine fin
// add. Сумма и дата — строки, как их набрал человек: разбирает их тот же код,
// что у команды. TZ — пояс человека (IANA, из браузера): в нем считается
// «сегодня» для даты по умолчанию и проверки даты из будущего; пусто — пояс
// сервера.
type TransactionAdd struct {
	Kind, Date, Amount, Category, Subcategory, Place, Description, Source, Account, Currency, TZ string
	Force                                                                                        bool
	// Received — вторая сторона перевода себе (обмен валюты); nil — ее нет.
	Received *ReceivedAdd
}

// ReceivedAdd is the second side as typed: суммы и валюта строками.
type ReceivedAdd struct {
	Amount, Currency, Account string
}

// Correction is a spelling the journal changed to the one already in use.
type Correction struct{ Field, Typed, Used string }

// TransactionAdded is the recorded operation. Warnings — то, о чем fin add
// предупреждает в stderr: запись состоялась, но человеку есть что поправить.
type TransactionAdded struct {
	ID, Date, Amount string
	Corrections      []Correction
	Warnings         []string
}

// BalanceConfirm is one confirmation of an account's balance: то же, что
// kbengine fin balance. Bank — из пути, остальное — из тела; сумма и курс —
// строки, как их набрал человек. TZ — как у TransactionAdd: день
// подтверждения — день человека.
type BalanceConfirm struct {
	Bank, Amount, Currency, Rate, TZ string
	Create                           bool
}

// BalanceConfirmed is the account as confirmed.
type BalanceConfirmed struct {
	Bank, Balance, Updated string
	Created                bool
}

// FinanceWriter records operations in the journal the server reads and
// confirms account balances. Правила записи — у реализации (cmd: те же, что у
// fin add и fin balance), обработчик только разбирает запрос и переводит
// ответ.
type FinanceWriter interface {
	AddTransaction(ctx context.Context, a TransactionAdd) (TransactionAdded, error)
	ConfirmBalance(ctx context.Context, c BalanceConfirm) (BalanceConfirmed, error)
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
	TZ          string `json:"tz"`
	Force       bool   `json:"force"`
	// Received — вторая сторона перевода себе, тем же объектом, что в журнале.
	Received *receivedAddDTO `json:"received"`
}

type receivedAddDTO struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Account  string `json:"account"`
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
	Warnings    []string        `json:"warnings,omitempty"`
}

func handleAddTransaction(fw FinanceWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in transactionAddDTO
		if err := decodeStrict(w, r, &in); err != nil {
			writeBodyError(w, err)
			return
		}
		got, err := fw.AddTransaction(r.Context(), TransactionAdd{
			Kind: in.Kind, Date: in.Date, Amount: in.Amount, Category: in.Category, Subcategory: in.Subcategory,
			Place: in.Place, Description: in.Description, Source: in.Source, Account: in.Account,
			Currency: in.Currency, TZ: in.TZ, Force: in.Force,
		})
		if err != nil {
			if errors.Is(err, ErrRepeat) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeWriteError(w, "journal", err)
			return
		}
		out := transactionAddedDTO{ID: got.ID, Date: got.Date, Amount: got.Amount, Warnings: got.Warnings}
		for _, c := range got.Corrections {
			out.Corrections = append(out.Corrections, correctionDTO(c))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(out)
	}
}

type balanceConfirmDTO struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Rate     string `json:"rate"`
	TZ       string `json:"tz"`
	Create   bool   `json:"create"`
}

type balanceConfirmedDTO struct {
	Bank    string `json:"bank"`
	Balance string `json:"balance"`
	Updated string `json:"updated"`
}

// handleConfirmBalance is PUT /api/finances/accounts/{name}/balance. Имя — из
// пути (PathValue уже раскодировал «Долг%20→%20Отец»). Новый счет — 201,
// подтверждение существующего — 200; незнакомое имя — 404 со списком
// известных, второй такой же — 409.
func handleConfirmBalance(fw FinanceWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in balanceConfirmDTO
		if err := decodeStrict(w, r, &in); err != nil {
			writeBodyError(w, err)
			return
		}
		got, err := fw.ConfirmBalance(r.Context(), BalanceConfirm{
			Bank: r.PathValue("name"), Amount: in.Amount, Currency: in.Currency, Rate: in.Rate, TZ: in.TZ, Create: in.Create,
		})
		switch {
		case errors.Is(err, domain.ErrUnknownAccount):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case errors.Is(err, domain.ErrAccountExists):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case err != nil:
			writeWriteError(w, "accounts", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if got.Created {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(balanceConfirmedDTO{Bank: got.Bank, Balance: got.Balance, Updated: got.Updated})
	}
}
