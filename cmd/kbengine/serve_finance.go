package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/financeaccounts"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financevocab"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// financeReadTimeout bounds one read of the journal: у порта Financier нет ctx.
const financeReadTimeout = 30 * time.Second

// storeFinances is a person's finances kept in a blob store: журнал операций,
// счета с подтвержденными остатками и словарь мест под users/<id>/fin/. Читает
// и пишет тем же разбором и теми же правилами, что команды fin, — financejsonl,
// planAdd и finance.ConfirmBalance; расчетный остаток считает тот же
// finance.CurrentBalances, что у книги на Маке.
//
// ponytail: журнал читается целиком на каждый запрос. Потолок — тысячи строк,
// сотни КБ на человека. Путь апгрейда — кэш по ETag, как у каталога
// (catalogjson.StoreLoader), и журнал по годам.
type storeFinances struct {
	ctx         context.Context
	store       blob.Store
	journalKey  string
	accountsKey string
	vocabKey    string
	now         func() time.Time
}

func (f storeFinances) records() ([]finance.Record, error) {
	ctx, cancel := context.WithTimeout(f.ctx, financeReadTimeout)
	defer cancel()
	o, err := f.store.Get(ctx, f.journalKey)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, nil // журнала еще нет — операций нет
	}
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	return financejsonl.Parse(f.journalKey, bytes.NewReader(o.Data), f.now)
}

// accounts reads the accounts. Документа нет — счетов нет; битый — отказ:
// без счетов расчет остатков молча показал бы ноль вместо денег.
func (f storeFinances) accounts(ctx context.Context) (financeaccounts.Book, error) {
	o, err := f.store.Get(ctx, f.accountsKey)
	if errors.Is(err, blob.ErrNotFound) {
		return financeaccounts.Parse(f.accountsKey, nil, f.now)
	}
	if err != nil {
		return financeaccounts.Book{}, fmt.Errorf("accounts: %w", err)
	}
	return financeaccounts.Parse(f.accountsKey, o.Data, f.now)
}

// Finances implements httpapi.Financier.
func (f storeFinances) Finances() (httpapi.Finances, error) {
	recs, err := f.records()
	if err != nil {
		return httpapi.Finances{}, err
	}
	ctx, cancel := context.WithTimeout(f.ctx, financeReadTimeout)
	defer cancel()
	book, err := f.accounts(ctx)
	if err != nil {
		return httpapi.Finances{}, err
	}
	out := httpapi.Finances{Transactions: make([]domain.Transaction, 0, len(recs)),
		Accounts: book.Accounts, Confirmations: book.Confirmations}
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
// частота; битый словарь — отказ записи (500, причина с ключом — в журнале
// сервера: чинит его владелец хранилища, а не автор операции), а не молча
// случайная подстановка.
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
// решение planAdd, условная запись журнала с повтором по свежему. Журнала нет
// — он заводится пустым и пишется той же условной записью.
//
// Снимка перед записью, как у файлового financejsonl.Save, здесь нет: эту роль
// играют версии бакета, они включены (outputs/2026-11-05_kb-server-plan.md).
func (f storeFinances) AddTransaction(ctx context.Context, a httpapi.TransactionAdd) (httpapi.TransactionAdded, error) {
	p, err := transactionParams(a)
	if err != nil {
		return httpapi.TransactionAdded{}, err
	}
	now, err := f.clockIn(a.TZ)
	if err != nil {
		return httpapi.TransactionAdded{}, err
	}
	voc, vocErr := f.vocabulary(ctx)
	if vocErr != nil && !errors.Is(vocErr, finance.ErrVocabularyConflict) {
		return httpapi.TransactionAdded{}, vocErr
	}
	// Счета — как у fin add с --from: валюта операции берется по счету и
	// сверяется с ним.
	book, err := f.accounts(ctx)
	if err != nil {
		return httpapi.TransactionAdded{}, err
	}
	p.Accounts = book.Accounts
	var warnings []string
	if errors.Is(vocErr, finance.ErrVocabularyConflict) {
		warnings = append(warnings, vocErr.Error())
	}
	var rec finance.Record
	var fixed []httpapi.Correction
	err = f.update(ctx, f.journalKey, func(raw []byte) ([]byte, error) {
		fixed = fixed[:0] // каждая попытка решает заново по свежему журналу
		recs, err := financejsonl.Parse(f.journalKey, bytes.NewReader(raw), f.now)
		if err != nil {
			return nil, err
		}
		var next []finance.Record
		rec, next, err = planAdd(recs, voc, vocErr, p, a.Force, now, func(c finance.Correction) {
			fixed = append(fixed, httpapi.Correction{Field: c.Field, Typed: c.Typed, Used: c.Used})
		}, nil) // спор словаря назван в ответе (warnings), а не в журнале сервера
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
	return httpapi.TransactionAdded{ID: tx.ID(), Date: tx.Date().Format(time.DateOnly), Amount: amountText(tx),
		Corrections: fixed, Warnings: warnings}, nil
}

// clockIn is the server clock read in the person's zone: день операции —
// день человека. Контейнер живет по UTC, и у человека в +05 после полуночи без
// этого сегодняшняя дата была бы «из будущего». Пусто — пояс сервера (TZ).
func (f storeFinances) clockIn(tz string) (func() time.Time, error) {
	if tz == "" {
		return f.now, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return nil, fmt.Errorf("%w: tz %q: expected an IANA zone such as Asia/Yekaterinburg", httpapi.ErrBadWrite, tz)
	}
	return func() time.Time { return f.now().In(loc) }, nil
}

// update is blob.Update over key, creating the document empty when it is
// missing: журнал и счета появляются у человека первой записью.
func (f storeFinances) update(ctx context.Context, key string, change func(raw []byte) ([]byte, error)) error {
	err := blob.Update(ctx, f.store, key, change)
	if !errors.Is(err, blob.ErrNotFound) {
		return err
	}
	// Одновременная первая запись уже завела документ — это не ошибка.
	if _, err := f.store.Put(ctx, key, nil, ""); err != nil && !errors.Is(err, blob.ErrConflict) {
		return fmt.Errorf("create %s: %w", key, err)
	}
	return blob.Update(ctx, f.store, key, change)
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

// ConfirmBalance implements httpapi.FinanceWriter: разбор как у fin balance,
// решение finance.ConfirmBalance, условная запись счетов с повтором по свежему.
// День и момент подтверждения — в поясе человека (tz), как у операций.
func (f storeFinances) ConfirmBalance(ctx context.Context, c httpapi.BalanceConfirm) (httpapi.BalanceConfirmed, error) {
	bad := func(err error) (httpapi.BalanceConfirmed, error) {
		return httpapi.BalanceConfirmed{}, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
	}
	if strings.TrimSpace(c.Amount) == "" {
		return bad(errors.New("amount is required"))
	}
	money, err := domain.ParseMoney(c.Amount)
	if err != nil {
		return bad(err)
	}
	change := finance.BalanceChange{Bank: c.Bank, Balance: money, Create: c.Create}
	if c.Currency != "" {
		cur, err := domain.NewCurrency(c.Currency)
		if err != nil {
			return bad(err)
		}
		change.Currency = &cur
	}
	if c.Rate != "" {
		per, err := domain.ParseMoney(c.Rate)
		if err != nil {
			return bad(fmt.Errorf("rate: %w", err))
		}
		if change.Rate, err = domain.NewRate(per); err != nil {
			return bad(err)
		}
	}
	now, err := f.clockIn(c.TZ)
	if err != nil {
		return httpapi.BalanceConfirmed{}, err
	}
	var confirmed domain.Account
	var created bool
	err = f.update(ctx, f.accountsKey, func(raw []byte) ([]byte, error) {
		book, err := financeaccounts.Parse(f.accountsKey, raw, f.now)
		if err != nil {
			return nil, err
		}
		accs, confs, err := finance.ConfirmBalance(book.Accounts, book.Confirmations, change, now)
		switch {
		case errors.Is(err, domain.ErrInvalidAccount):
			return nil, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
		case err != nil:
			return nil, err // ErrUnknownAccount, ErrAccountExists — обработчик знает их коды
		}
		created = len(accs) > len(book.Accounts)
		for _, a := range accs {
			if domain.SameAccountName(a.Bank(), c.Bank) {
				confirmed = a
			}
		}
		return financeaccounts.Encode(financeaccounts.Book{Accounts: accs, Confirmations: confs})
	})
	if err != nil {
		return httpapi.BalanceConfirmed{}, err
	}
	return httpapi.BalanceConfirmed{Bank: confirmed.Bank(), Balance: confirmed.Balance().String(),
		Updated: confirmed.Updated().Format(time.DateOnly), Created: created}, nil
}
