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

// latestZoneOffset is the offset of the zone whose day comes first on Earth (UTC+14, Kiritimati).
const latestZoneOffset = 14 * 60 * 60

// readNow is the clock for reading what was already written. Запись проверяет
// «дату из будущего» часами человека (clockIn), а чтение идет часами сервера,
// который живет по UTC: у человека в +05 после полуночи принятая запись была
// бы «из будущего» до полуночи UTC, и журнал со счетами не читались бы часами.
// Поэтому при чтении «будущее» — это позже, чем сегодня где угодно на Земле:
// принятое в любом настоящем поясе читается, опечатка в годе — по-прежнему нет.
func (f storeFinances) readNow() time.Time {
	return f.now().In(time.FixedZone("UTC+14", latestZoneOffset))
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
	return financejsonl.Parse(f.journalKey, bytes.NewReader(o.Data), f.readNow)
}

// accounts reads the accounts. Документа нет — счетов нет; битый — отказ:
// без счетов расчет остатков молча показал бы ноль вместо денег.
func (f storeFinances) accounts(ctx context.Context) (financeaccounts.Book, error) {
	o, err := f.store.Get(ctx, f.accountsKey)
	if errors.Is(err, blob.ErrNotFound) {
		return financeaccounts.Parse(f.accountsKey, nil, f.readNow)
	}
	if err != nil {
		return financeaccounts.Book{}, fmt.Errorf("accounts: %w", err)
	}
	return financeaccounts.Parse(f.accountsKey, o.Data, f.readNow)
}

// accountsFor reads the accounts when an operation names one — to send from or
// to receive on — and returns nil otherwise.
// Прочитанные счета не бывают nil, даже когда их нет: nil значит «книга не под
// рукой» (fin add без --from), и незнакомый счет получения тогда не проверялся
// бы — а в хранилище счета человека известны всегда, пусть и пустым списком.
func (f storeFinances) accountsFor(ctx context.Context, a httpapi.TransactionAdd) ([]domain.Account, error) {
	if strings.TrimSpace(a.Account) == "" && a.Received == nil {
		return nil, nil // ни счета списания, ни счета получения
	}
	book, err := f.accounts(ctx)
	if err != nil {
		return nil, err
	}
	if book.Accounts == nil {
		return []domain.Account{}, nil
	}
	return book.Accounts, nil
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
	// сверяется с ним. Только когда счет назван: трате наличными документ
	// счетов не нужен, и его сбой не должен ее останавливать.
	//
	// ponytail: счета читаются один раз, до цикла условной записи журнала, а
	// не внутри него: журнал и счета — два документа без общей транзакции, и
	// смена валюты счета ровно в момент траты с него запишет трату в прежней
	// валюте. Потолок — окно в доли секунды при одном человеке на счет. Путь
	// апгрейда — номер версии счетов в записи и перепроверка после записи.
	if p.Accounts, err = f.accountsFor(ctx, a); err != nil {
		return httpapi.TransactionAdded{}, err
	}
	var warnings []string
	if errors.Is(vocErr, finance.ErrVocabularyConflict) {
		warnings = append(warnings, vocErr.Error())
	}
	var rec finance.Record
	var fixed []httpapi.Correction
	err = f.update(ctx, f.journalKey, func(raw []byte) ([]byte, error) {
		fixed = fixed[:0] // каждая попытка решает заново по свежему журналу
		recs, err := financejsonl.Parse(f.journalKey, bytes.NewReader(raw), f.readNow)
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
	received, err := receivedAdd(a.Received)
	if err != nil {
		return bad(err)
	}
	return finance.AddParams{
		Kind: kind, Date: when, Amount: money, Category: a.Category, Subcategory: a.Subcategory,
		Place: a.Place, Description: a.Description, Source: a.Source, Account: a.Account, Currency: named,
		Received: received,
	}, nil
}

// receivedAdd parses the second side of a transfer to self, nil when there is none.
func receivedAdd(r *httpapi.ReceivedAdd) (*finance.ReceivedAdd, error) {
	if r == nil {
		return nil, nil
	}
	if strings.TrimSpace(r.Amount) == "" {
		return nil, errors.New("received: amount is required")
	}
	money, err := domain.ParseMoney(r.Amount)
	if err != nil {
		return nil, fmt.Errorf("received: %w", err)
	}
	named, err := currencyFlag(r.Currency)
	if err != nil {
		return nil, fmt.Errorf("received: %w", err)
	}
	return &finance.ReceivedAdd{Amount: money, Currency: named, Account: r.Account}, nil
}

// ConfirmBalance implements httpapi.FinanceWriter: разбор как у fin balance,
// решение finance.ConfirmBalance, условная запись счетов с повтором по свежему.
// День и момент подтверждения — в поясе человека (tz), как у операций.
func (f storeFinances) ConfirmBalance(ctx context.Context, c httpapi.BalanceConfirm) (httpapi.BalanceConfirmed, error) {
	change, err := balanceChange(c)
	if err != nil {
		return httpapi.BalanceConfirmed{}, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
	}
	now, err := f.clockIn(c.TZ)
	if err != nil {
		return httpapi.BalanceConfirmed{}, err
	}
	var confirmed domain.Account
	var created bool
	err = f.update(ctx, f.accountsKey, func(raw []byte) ([]byte, error) {
		book, err := financeaccounts.Parse(f.accountsKey, raw, f.readNow)
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

// balanceChange parses the request the way fin balance parses its flags.
func balanceChange(c httpapi.BalanceConfirm) (finance.BalanceChange, error) {
	if strings.TrimSpace(c.Amount) == "" {
		return finance.BalanceChange{}, errors.New("amount is required")
	}
	money, err := domain.ParseMoney(c.Amount)
	if err != nil {
		return finance.BalanceChange{}, err
	}
	change := finance.BalanceChange{Bank: c.Bank, Balance: money, Create: c.Create}
	if c.Currency != "" {
		cur, err := domain.NewCurrency(c.Currency)
		if err != nil {
			return finance.BalanceChange{}, err
		}
		change.Currency = &cur
	}
	if c.Rate != "" {
		per, err := domain.ParseMoney(c.Rate)
		if err != nil {
			return finance.BalanceChange{}, fmt.Errorf("rate: %w", err)
		}
		if change.Rate, err = domain.NewRate(per); err != nil {
			return finance.BalanceChange{}, err
		}
	}
	return change, nil
}
