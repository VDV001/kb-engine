package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financevocab"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// financeReadTimeout bounds one read of the journal: у порта Financier нет ctx.
const financeReadTimeout = 30 * time.Second

// storeFinances is a person's finances kept in a blob store: журнал операций и
// словарь мест под users/<id>/fin/. Читает и пишет тем же разбором и теми же
// правилами, что команды fin, — financejsonl и planAdd.
//
// Счетов и подтвержденных остатков здесь пока нет (этап E2): /api/finances
// отдает операции и итоги, без балансов.
//
// ponytail: журнал читается целиком на каждый запрос. Потолок — тысячи строк,
// сотни КБ на человека. Путь апгрейда — кэш по ETag, как у каталога
// (catalogjson.StoreLoader), и журнал по годам.
type storeFinances struct {
	ctx        context.Context
	store      blob.Store
	journalKey string
	vocabKey   string
	now        func() time.Time
}

func (f storeFinances) records() ([]finance.Record, error) {
	ctx, cancel := context.WithTimeout(f.ctx, financeReadTimeout)
	defer cancel()
	o, err := f.store.Get(ctx, f.journalKey)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	return financejsonl.Parse(f.journalKey, bytes.NewReader(o.Data), f.now)
}

// Finances implements httpapi.Financier.
func (f storeFinances) Finances() (httpapi.Finances, error) {
	recs, err := f.records()
	if err != nil {
		return httpapi.Finances{}, err
	}
	out := httpapi.Finances{Transactions: make([]domain.Transaction, 0, len(recs))}
	for _, r := range recs {
		out.Transactions = append(out.Transactions, r.Transaction())
	}
	return out, nil
}

// Summary implements httpapi.Financier.
func (f storeFinances) Summary(months []string) (finance.Summary, error) {
	recs, err := f.records()
	if err != nil {
		return finance.Summary{}, err
	}
	return finance.Summarize(finance.Match(recs, finance.Filter{Months: months})), nil
}

// vocabulary reads the place dictionary. Нет словаря — не беда, решает
// частота; битый словарь — отказ записи с причиной, а не молча случайная
// подстановка.
func (f storeFinances) vocabulary(ctx context.Context) (finance.Vocabulary, error) {
	empty := finance.Vocabulary{Accounts: map[string]string{}, Places: map[string]finance.PlaceRule{}}
	o, err := f.store.Get(ctx, f.vocabKey)
	if errors.Is(err, blob.ErrNotFound) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("vocabulary: %w", err)
	}
	return financevocab.Parse(f.vocabKey, o.Data)
}

// AddTransaction implements httpapi.FinanceWriter: разбор как у fin add,
// решение planAdd, условная запись журнала с повтором по свежему.
func (f storeFinances) AddTransaction(ctx context.Context, a httpapi.TransactionAdd) (httpapi.TransactionAdded, error) {
	p, err := transactionParams(a)
	if err != nil {
		return httpapi.TransactionAdded{}, err
	}
	voc, vocErr := f.vocabulary(ctx)
	if vocErr != nil && !errors.Is(vocErr, finance.ErrVocabularyConflict) {
		return httpapi.TransactionAdded{}, vocErr
	}
	var rec finance.Record
	var fixed []httpapi.Correction
	err = blob.Update(ctx, f.store, f.journalKey, func(raw []byte) ([]byte, error) {
		fixed = fixed[:0] // каждая попытка решает заново по свежему журналу
		recs, err := financejsonl.Parse(f.journalKey, bytes.NewReader(raw), f.now)
		if err != nil {
			return nil, err
		}
		var next []finance.Record
		rec, next, err = planAdd(recs, voc, vocErr, p, a.Force, func(c finance.Correction) {
			fixed = append(fixed, httpapi.Correction{Field: c.Field, Typed: c.Typed, Used: c.Used})
		}, nil)
		switch {
		case errors.Is(err, ErrRepeat):
			return nil, fmt.Errorf("%w: %w", httpapi.ErrRepeat, err)
		case err != nil:
			return nil, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
		}
		var buf bytes.Buffer
		if err := financejsonl.Encode(&buf, next); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	})
	if err != nil {
		return httpapi.TransactionAdded{}, err
	}
	tx := rec.Transaction()
	return httpapi.TransactionAdded{ID: tx.ID(), Date: tx.Date().Format(time.DateOnly), Amount: amountText(tx), Corrections: fixed}, nil
}

// transactionParams parses the request the way fin add parses its flags.
func transactionParams(a httpapi.TransactionAdd) (finance.AddParams, error) {
	bad := func(err error) (finance.AddParams, error) {
		return finance.AddParams{}, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
	}
	if strings.TrimSpace(a.Amount) == "" {
		return bad(errors.New("amount is required"))
	}
	money, err := domain.ParseMoney(a.Amount)
	if err != nil {
		return bad(err)
	}
	var when time.Time
	if a.Date != "" {
		if when, err = time.Parse(time.DateOnly, a.Date); err != nil {
			return bad(fmt.Errorf("date %q: expected YYYY-MM-DD", a.Date))
		}
	}
	named, err := currencyFlag(a.Currency)
	if err != nil {
		return bad(err)
	}
	kind := a.Kind
	if kind == "" {
		kind = domain.KindExpense
	}
	return finance.AddParams{
		Kind: kind, Date: when, Amount: money, Category: a.Category, Subcategory: a.Subcategory,
		Place: a.Place, Description: a.Description, Source: a.Source, Account: a.Account, Currency: named,
	}, nil
}
