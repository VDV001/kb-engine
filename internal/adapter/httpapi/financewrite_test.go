package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
)

type fakeFinWriter struct {
	got     httpapi.TransactionAdd
	gotConf httpapi.BalanceConfirm
	err     error
}

func (f *fakeFinWriter) ConfirmBalance(_ context.Context, c httpapi.BalanceConfirm) (httpapi.BalanceConfirmed, error) {
	f.gotConf = c
	if f.err != nil {
		return httpapi.BalanceConfirmed{}, f.err
	}
	return httpapi.BalanceConfirmed{Bank: "Долг → Сосед", Balance: "-5000.00", Updated: "2026-11-06", Created: c.Create}, nil
}

func (f *fakeFinWriter) AddTransaction(_ context.Context, a httpapi.TransactionAdd) (httpapi.TransactionAdded, error) {
	f.got = a
	if f.err != nil {
		return httpapi.TransactionAdded{}, f.err
	}
	return httpapi.TransactionAdded{ID: "01J", Date: "2026-11-05", Amount: "418.00",
		Corrections: []httpapi.Correction{{Field: "place", Typed: "пятерочка", Used: "Пятёрочка"}}}, nil
}

func finServer(w httpapi.FinanceWriter) http.Handler {
	var opts []httpapi.Option
	if w != nil {
		opts = append(opts, httpapi.WithFinanceWriter(w))
	}
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil, opts...)
}

func TestPostTransaction(t *testing.T) {
	fw := &fakeFinWriter{}
	rec := send(finServer(fw), http.MethodPost, "/api/finances/transactions",
		`{"kind":"expense","amount":"418","category":"Транспорт","place":"пятерочка","account":"Сбербанк","date":"2026-11-05"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"id":"01J"`, `"typed":"пятерочка"`, `"used":"Пятёрочка"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, rec.Body.String())
		}
	}
	if fw.got.Amount != "418" || fw.got.Place != "пятерочка" || fw.got.Force {
		t.Fatalf("writer got %+v", fw.got)
	}
}

func TestTransactionErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		body string // часть ответа, которую человек обязан увидеть
		log  string // часть причины, которая обязана остаться в журнале сервера
	}{
		{"repeat without force", fmt.Errorf("%w: 2026-11-05 · 418 ₽", httpapi.ErrRepeat), http.StatusConflict, "418", ""},
		{"rule refused", fmt.Errorf("%w: сумма", httpapi.ErrBadWrite), http.StatusBadRequest, "сумма", ""},
		{"journal kept changing", fmt.Errorf("users/u1/fin/journal.jsonl: %w", blob.ErrConflict), http.StatusConflict, "journal", ""},
		{"store down", errors.New("blobs3: 503"), http.StatusInternalServerError, "write failed", "blobs3: 503"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logged bytes.Buffer
			log.SetOutput(&logged)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			rec := send(finServer(&fakeFinWriter{err: c.err}), http.MethodPost, "/api/finances/transactions", `{"amount":"1"}`)
			if rec.Code != c.code {
				t.Fatalf("%d, want %d", rec.Code, c.code)
			}
			if !strings.Contains(rec.Body.String(), c.body) {
				t.Errorf("body %q lacks %q", rec.Body.String(), c.body)
			}
			if !strings.Contains(logged.String(), c.log) {
				t.Errorf("server log %q lacks %q", logged.String(), c.log)
			}
		})
	}
	h := finServer(&fakeFinWriter{})
	if rec := send(h, http.MethodPost, "/api/finances/transactions", `{"amount":"1","nope":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
	if rec := plainPost(h, "/api/finances/transactions", `{"amount":"1"}`); rec != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d, want 415", rec)
	}
	if rec := send(finServer(nil), http.MethodPost, "/api/finances/transactions", `{"amount":"1"}`); rec.Code < 400 || rec.Code == http.StatusCreated {
		t.Errorf("without a writer: %d", rec.Code)
	}
}

func plainPost(h http.Handler, path, body string) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, newPlain(path, body))
	return w.Code
}

func newPlain(path, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	return req
}

// Подтверждение остатка: имя счета — из пути, со стрелкой и пробелами, как его
// пишет книга; заведение нового — 201, подтверждение существующего — 200.
func TestConfirmBalance(t *testing.T) {
	fw := &fakeFinWriter{}
	path := "/api/finances/accounts/" + url.PathEscape("Долг → Сосед") + "/balance"
	rec := send(finServer(fw), http.MethodPut, path, `{"amount":"-5000","tz":"Asia/Yekaterinburg"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"bank":"Долг → Сосед"`, `"balance":"-5000.00"`, `"updated":"2026-11-06"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, rec.Body.String())
		}
	}
	if fw.gotConf.Bank != "Долг → Сосед" || fw.gotConf.Amount != "-5000" || fw.gotConf.TZ != "Asia/Yekaterinburg" || fw.gotConf.Create {
		t.Fatalf("writer got %+v", fw.gotConf)
	}
	if rec := send(finServer(fw), http.MethodPut, path, `{"amount":"1","create":true,"currency":"USD","rate":"84,28"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if fw.gotConf.Currency != "USD" || fw.gotConf.Rate != "84,28" || !fw.gotConf.Create {
		t.Fatalf("writer got %+v", fw.gotConf)
	}
}

func TestConfirmBalanceErrors(t *testing.T) {
	path := "/api/finances/accounts/" + url.PathEscape("Сбербанк") + "/balance"
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"unknown account", fmt.Errorf("%w: %q — known: Т-Банк", domain.ErrUnknownAccount, "Сбербанк"), http.StatusNotFound, "Т-Банк"},
		{"account already there", fmt.Errorf("%w: %q", domain.ErrAccountExists, "Сбербанк"), http.StatusConflict, "Сбербанк"},
		{"bad amount", fmt.Errorf("%w: amount", httpapi.ErrBadWrite), http.StatusBadRequest, "amount"},
		{"accounts kept changing", fmt.Errorf("users/u1/fin/accounts.json: %w", blob.ErrConflict), http.StatusConflict, "accounts"},
		{"store down", errors.New("blobs3: 503"), http.StatusInternalServerError, "write failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := send(finServer(&fakeFinWriter{err: c.err}), http.MethodPut, path, `{"amount":"1"}`)
			if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
				t.Fatalf("%d %q, want %d with %q", rec.Code, rec.Body.String(), c.code, c.body)
			}
		})
	}
	h := finServer(&fakeFinWriter{})
	if rec := send(h, http.MethodPut, path, `{"amount":"1","nope":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
	req, _ := http.NewRequest(http.MethodPut, path, strings.NewReader(`{"amount":"1"}`))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d, want 415", w.Code)
	}
	if rec := send(finServer(nil), http.MethodPut, path, `{"amount":"1"}`); rec.Code < 400 {
		t.Errorf("without a writer: %d", rec.Code)
	}
}
