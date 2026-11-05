// Package financeaccounts reads and writes a person's accounts kept as JSON:
// счета, валюта, курс, подтвержденный остаток, его день и момент. На Маке это
// лист «Счета» книги плюс .balance-state.json; в хранилище — один документ
// users/<id>/fin/accounts.json, который пишется одной условной записью.
package financeaccounts

import (
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

// Parse decodes the document read from name. Пустой документ — пустая книга.
func Parse(name string, raw []byte, now func() time.Time) (Book, error) {
	return Book{}, nil
}

// Encode writes b as the document.
func Encode(b Book) ([]byte, error) {
	return nil, nil
}
