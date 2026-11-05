// Package financeaccounts reads and writes a person's accounts kept as JSON:
// счета, валюта, курс, подтвержденный остаток, его день и момент. На Маке это
// лист «Счета» книги плюс .balance-state.json; в хранилище — один документ
// users/<id>/fin/accounts.json, который пишется одной условной записью, и
// день с моментом не могут разойтись.
package financeaccounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// FileName is the document's name beside the journal.
const FileName = "accounts.json"

// Book is the accounts and the moments their balances were confirmed.
type Book struct {
	Accounts      []domain.Account
	Confirmations finance.Confirmations
}

type document struct {
	Accounts []accountDTO `json:"accounts"`
}

// accountDTO is one account. Суммы — строки, как в журнале: float потерял бы
// копейки. Updated — день, который читает человек; ConfirmedAt — момент,
// который решает спор о тратах того же дня (пусто — момент неизвестен).
type accountDTO struct {
	Bank        string `json:"bank"`
	Balance     string `json:"balance"`
	Updated     string `json:"updated,omitempty"`
	ConfirmedAt string `json:"confirmed_at,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Rate        string `json:"rate,omitempty"`
}

// Parse decodes the document read from name. Пустой документ — пустая книга:
// так выглядят счета человека, у которого их еще нет. Любой отказ — ErrMalformed:
// битый документ — сбой хранилища, а не ответ на запрос, поэтому и два
// написания одного счета здесь не domain.ErrAccountExists (у API это 409).
//
// now — часы чтения. Дни в документе записаны в поясе человека, поэтому читать
// их надо часами самого позднего пояса (см. storeFinances.readNow), иначе
// принятое при записи восточнее UTC окажется «из будущего».
func Parse(name string, raw []byte, now func() time.Time) (Book, error) {
	b := Book{Confirmations: finance.Confirmations{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return b, nil
	}
	var doc document
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Опечатка в имени поля — отказ, а не молча пропавший курс.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return Book{}, fmt.Errorf("%w: %s: %w", ErrMalformed, name, err)
	}
	for _, d := range doc.Accounts {
		acc, err := d.account(now)
		if err != nil {
			return Book{}, fmt.Errorf("%w: %s: account %q: %w", ErrMalformed, name, d.Bank, err)
		}
		if slices.ContainsFunc(b.Accounts, func(a domain.Account) bool { return domain.SameAccountName(a.Bank(), acc.Bank()) }) {
			return Book{}, fmt.Errorf("%w: %s: two spellings of one account: %q", ErrMalformed, name, d.Bank)
		}
		b.Accounts = append(b.Accounts, acc)
		if d.ConfirmedAt != "" {
			at, err := time.Parse(time.RFC3339Nano, d.ConfirmedAt)
			if err != nil {
				return Book{}, fmt.Errorf("%w: %s: account %q: confirmed_at: %w", ErrMalformed, name, d.Bank, err)
			}
			b.Confirmations[acc.Bank()] = at
		}
	}
	return b, nil
}

func (d accountDTO) account(now func() time.Time) (domain.Account, error) {
	balance, err := domain.ParseMoney(d.Balance)
	if err != nil {
		return domain.Account{}, fmt.Errorf("balance: %w", err)
	}
	var updated time.Time
	if d.Updated != "" {
		if updated, err = time.Parse(time.DateOnly, d.Updated); err != nil {
			return domain.Account{}, fmt.Errorf("updated %q: expected YYYY-MM-DD", d.Updated)
		}
	}
	var currency domain.Currency
	if d.Currency != "" {
		if currency, err = domain.NewCurrency(d.Currency); err != nil {
			return domain.Account{}, err
		}
	}
	rate := domain.UnknownRate()
	if d.Rate != "" {
		if d.Currency == "" {
			return domain.Account{}, fmt.Errorf("%w: a rate without a currency", domain.ErrInvalidAccount)
		}
		per, err := domain.ParseMoney(d.Rate)
		if err != nil {
			return domain.Account{}, fmt.Errorf("rate: %w", err)
		}
		if rate, err = domain.NewRate(per); err != nil {
			return domain.Account{}, err
		}
	}
	return domain.NewForeignAccount(d.Bank, balance, currency, rate, updated, now)
}

// Encode writes b as the document. День пишется в поясе, в котором его
// подтвердили (день человека), момент — в UTC.
func Encode(b Book) ([]byte, error) {
	doc := document{Accounts: make([]accountDTO, 0, len(b.Accounts))}
	for _, a := range b.Accounts {
		d := accountDTO{Bank: a.Bank(), Balance: a.Balance().String()}
		if !a.Updated().IsZero() {
			d.Updated = a.Updated().Format(time.DateOnly)
		}
		if at, ok := b.Confirmations.At(a.Bank()); ok {
			d.ConfirmedAt = at.UTC().Format(time.RFC3339Nano)
		}
		if !a.Currency().IsBase() {
			d.Currency = a.Currency().Code()
		}
		if per, ok := a.Rate().PerUnit(); ok {
			d.Rate = per.String()
		}
		doc.Accounts = append(doc.Accounts, d)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // «Долг → Сосед», а не →
	enc.SetIndent("", "  ")  // документ читают глазами, скачав из бакета
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ErrMalformed — документ счетов не читается: сбой хранилища, а не ответ на
// запрос человека.
var ErrMalformed = errors.New("malformed accounts document")
