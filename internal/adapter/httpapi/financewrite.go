package httpapi

import (
	"context"
	"errors"
)

var ErrRepeat = errors.New("repeat")

type TransactionAdd struct {
	Kind, Date, Amount, Category, Subcategory, Place, Description, Source, Account, Currency string
	Force                                                                                    bool
}

type Correction struct{ Field, Typed, Used string }

type TransactionAdded struct {
	ID, Date, Amount string
	Corrections      []Correction
}

type FinanceWriter interface {
	AddTransaction(ctx context.Context, a TransactionAdd) (TransactionAdded, error)
}

func WithFinanceWriter(FinanceWriter) Option { return func(*options) {} }
