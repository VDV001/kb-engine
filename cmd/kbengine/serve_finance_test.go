package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync`
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// financeTenant registers one person and returns the server, the token and the
// store.
func financeTenant(t *testing.T) (http.Handler, string, *blobfs.Store) {
	t.Helper()
	h, tok, s, _ := financeTenantIn(t, t.TempDir(), nil)
	return h, tok, s
}

// financeTenantIn is financeTenant over dir, with the store optionally wrapped
// (сбои чтения). Возвращает и корень хранилища на диске.
func financeTenantIn(t *testing.T, dir string, wrap func(blob.Store) blob.Store) (http.Handler, string, *blobfs.Store, string) {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.NewUserID("anna")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := userauth.Add(ctx, s, uid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/anna/fin/finance-aliases.json", []byte(`{"places":{"пятерочка":{"place":"Пятёрочка","category":"Еда"}}}`), ""); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	var store blob.Store = s
	if wrap != nil {
		store = wrap(s)
	}
	return tenantsHandler(ctx, store, &stderr), tok, s, dir
}

func transactions(t *testing.T, h http.Handler, tok string) []map[string]any {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/finances", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/finances: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%.200s)", err, rec.Body.String())
	}
	return out.Transactions
}

// Операция через API идет правилами fin add: словарь мест подставляет
// написание и говорит об этом, повтор без force — отказ, с force — запись.
func TestTransactionThroughTheAPI(t *testing.T) {
	h, tok, _ := financeTenant(t)
	body := `{"kind":"expense","amount":"418","category":"Еда","place":"пятерочка","date":"2026-04-06"}`
	rec := do(h, http.MethodPost, "/api/finances/transactions", body, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"used":"Пятёрочка"`) {
		t.Errorf("the vocabulary substitution is not reported: %s", rec.Body.String())
	}
	if got := transactions(t, h, tok); len(got) != 1 {
		t.Fatalf("%d transactions after one add", len(got))
	}
	if rec := do(h, http.MethodPost, "/api/finances/transactions", body, tok); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "force") {
		t.Fatalf("repeat without force: %d %s", rec.Code, rec.Body.String())
	}
	forced := strings.Replace(body, `{`, `{"force":true,`, 1)
	if rec := do(h, http.MethodPost, "/api/finances/transactions", forced, tok); rec.Code != http.StatusCreated {
		t.Fatalf("repeat with force: %d %s", rec.Code, rec.Body.String())
	}
	if got := transactions(t, h, tok); len(got) != 2 {
		t.Fatalf("%d transactions after a forced repeat", len(got))
	}
	for _, bad := range []string{
		`{"kind":"expense","amount":"abc","category":"Еда"}`,
		`{"kind":"expense","amount":"1.001","category":"Еда"}`,
		`{"kind":"expense","amount":"1","category":"Еда","date":"05.11.2026"}`,
	} {
		if rec := do(h, http.MethodPost, "/api/finances/transactions", bad, tok); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
}

// Одновременные операции не теряются: при конфликте журнал перечитывается, и
// проверка повтора судит по свежему.
func TestConcurrentTransactionsAllLand(t *testing.T) {
	h, tok, _ := financeTenant(t)
	const n = 6
	var wg gosync.WaitGroup
	for i := range n {
		wg.Go(func() {
			rec := do(h, http.MethodPost, "/api/finances/transactions",
				fmt.Sprintf(`{"kind":"expense","amount":"%d","category":"Еда","date":"2026-04-06"}`, 100+i), tok)
			if rec.Code != http.StatusCreated {
				t.Errorf("POST %d: %d %s", i, rec.Code, rec.Body.String())
			}
		})
	}
	wg.Wait()
	if got := transactions(t, h, tok); len(got) != n {
		t.Fatalf("%d transactions after %d concurrent adds", len(got), n)
	}
}

// Финансы есть у каждого, даже если журнала еще нет: человек, заведенный до
// журналов, видит пустой список, а первая операция заводит журнал — без
// перезапуска сервера.
func TestFinancesWithoutAJournalYet(t *testing.T) {
	h, tok, s, dir := financeTenantIn(t, t.TempDir(), nil)
	if err := os.Remove(filepath.Join(dir, "users", "anna", "fin", "journal.jsonl")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if got := transactions(t, h, tok); len(got) != 0 {
		t.Fatalf("%d transactions without a journal", len(got))
	}
	rec := do(h, http.MethodPost, "/api/finances/transactions", `{"kind":"expense","amount":"418","category":"Еда","date":"2026-04-06"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first POST without a journal: %d %s", rec.Code, rec.Body.String())
	}
	if got := transactions(t, h, tok); len(got) != 1 {
		t.Fatalf("%d transactions after the first add", len(got))
	}
	if _, err := s.Get(context.Background(), "users/anna/fin/journal.jsonl"); err != nil {
		t.Fatalf("the journal was not created: %v", err)
	}
}

// failingJournal fails every read of a journal: временный сбой хранилища.
type failingJournal struct{ blob.Store }

func (f failingJournal) Get(ctx context.Context, key string) (blob.Object, error) {
	if strings.HasSuffix(key, "/fin/journal.jsonl") {
		return blob.Object{}, errors.New("503 slow down")
	}
	return f.Store.Get(ctx, key)
}

// Сбой журнала — это сбой финансов, а не всей базы человека: поиск и записи
// работают, отказывает только финансовый вид.
func TestJournalFailureLeavesTheCatalog(t *testing.T) {
	h, tok, _, _ := financeTenantIn(t, t.TempDir(), func(s blob.Store) blob.Store { return failingJournal{s} })
	if rec := do(h, http.MethodGet, "/api/stats", "", tok); rec.Code != http.StatusOK {
		t.Fatalf("/api/stats while the journal fails: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/finances", "", tok); rec.Code == http.StatusOK {
		t.Fatalf("/api/finances answered 200 while its journal fails")
	}
}

// Словарь, где два написания спорят о правиле, не повод молчать: операция
// записывается, как у fin add, а спор назван в ответе.
func TestVocabularyConflictIsReported(t *testing.T) {
	h, tok, s := financeTenant(t)
	ctx := context.Background()
	cur, err := s.Get(ctx, "users/anna/fin/finance-aliases.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/anna/fin/finance-aliases.json",
		[]byte(`{"places":{"пятерочка":{"place":"Пятёрочка","category":"Еда"},"ПЯТЕРОЧКА":{"place":"Пятёрочка","category":"Дом"}}}`), cur.ETag); err != nil {
		t.Fatal(err)
	}
	rec := do(h, http.MethodPost, "/api/finances/transactions", `{"kind":"expense","amount":"418","category":"Еда","place":"пятерочка","date":"2026-04-06"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"warnings":[`) || !strings.Contains(rec.Body.String(), "спорят") {
		t.Fatalf("the vocabulary conflict is not reported: %s", rec.Body.String())
	}
}

// С --ledger финансы целиком из файла: и чтение, и запись. Иначе POST писал бы
// в журнал бакета, а GET и проверка повтора читали бы файл — два журнала.
func TestLedgerFlagOwnsFinanceWrites(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.NewUserID("anna")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userauth.Add(ctx, s, uid); err != nil {
		t.Fatal(err)
	}
	src, err := storeCatalogSource(ctx, s, userauth.CatalogKey(uid), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(ledger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := buildServeHandlerFrom(src, "", ledger, "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(h, http.MethodPost, "/api/finances/transactions", `{"kind":"expense","amount":"418","category":"Еда","date":"2026-04-06"}`, "")
	if rec.Code == http.StatusCreated {
		t.Errorf("POST with --ledger wrote to the store journal: %d %s", rec.Code, rec.Body.String())
	}
	if o, err := s.Get(ctx, "users/anna/fin/journal.jsonl"); err == nil && len(o.Data) != 0 {
		t.Errorf("the store journal changed under --ledger: %q", o.Data)
	}
}

// День операции — день человека, а не сервера: контейнер живет по UTC, а
// человек в +05 после полуночи уже в следующем дне. Пояс присылает страница
// (tz), без него — пояс сервера.
func TestTransactionDayIsThePersonsDay(t *testing.T) {
	// 20:30 UTC 5 ноября: в Екатеринбурге уже 01:30 6 ноября, на Мидуэе 09:30 5-го.
	serverNow := func() time.Time { return time.Date(2026, 11, 5, 20, 30, 0, 0, time.UTC) }
	cases := []struct {
		name, tz, date string
		code           int
		want           string // почему именно такой ответ: записанная дата или причина отказа
	}{
		{"today in the person's zone", "Asia/Yekaterinburg", "2026-11-06", http.StatusCreated, `"date":"2026-11-06"`},
		{"no date takes the person's today", "Asia/Yekaterinburg", "", http.StatusCreated, `"date":"2026-11-06"`},
		{"west of UTC it is still the 5th", "Pacific/Midway", "2026-11-05", http.StatusCreated, `"date":"2026-11-05"`},
		{"west of UTC the 6th is the future", "Pacific/Midway", "2026-11-06", http.StatusBadRequest, "in the future"},
		{"no zone falls back to the server's", "", "2026-11-06", http.StatusBadRequest, "in the future"},
		{"unknown zone is refused", "Mars/Olympus", "2026-11-05", http.StatusBadRequest, "Mars/Olympus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := blobfs.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sf := storeFinances{ctx: context.Background(), store: s, journalKey: "users/anna/fin/journal.jsonl", accountsKey: "users/anna/fin/accounts.json",
				vocabKey: "users/anna/fin/finance-aliases.json", now: serverNow}
			h := httpapi.NewServer(nil, nil, nil, nil, nil, nil, httpapi.Documents{}, httpapi.EngineInfo{}, nil, httpapi.WithFinanceWriter(sf))
			body := fmt.Sprintf(`{"kind":"expense","amount":"418","category":"Еда","date":%q,"tz":%q}`, c.date, c.tz)
			rec := do(h, http.MethodPost, "/api/finances/transactions", body, "")
			if rec.Code != c.code {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body.String(), c.code)
			}
			if !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("body %s lacks %s", rec.Body.String(), c.want)
			}
		})
	}
}

// Финансы ищутся только там, где их место однозначно: рядом с базой человека
// <человек>/kb/_data/catalog.json → <человек>/fin/. При другой раскладке папка
// угадывалась бы по глубине ключа, и две базы a/_data и b/_data делили бы один
// журнал fin/ в корне бакета — финансов у такой базы нет.
func TestFinancesOnlyBesideAPersonsBase(t *testing.T) {
	cases := []struct {
		key     string
		journal string // "" — финансов нет
	}{
		{"users/anna/kb/_data/catalog.json", "users/anna/fin/journal.jsonl"},
		{"kb/_data/catalog.json", "fin/journal.jsonl"},
		{"a/_data/catalog.json", ""},
		{"_data/catalog.json", ""},
		{"catalog.json", ""},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			ctx := context.Background()
			s, err := blobfs.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Put(ctx, c.key, []byte(`{"entries":[]}`), ""); err != nil {
				t.Fatal(err)
			}
			src, err := storeCatalogSource(ctx, s, c.key, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if sf, ok := src.finWriter.(storeFinances); ok {
				got = sf.journalKey
			}
			if got != c.journal || (src.fin == nil) != (c.journal == "") {
				t.Fatalf("journal %q (fin %v), want %q", got, src.fin != nil, c.journal)
			}
		})
	}
}

// Счета человека в хранилище: заведение и подтверждение остатка через API,
// расчетный остаток тем же правилом, что у книги на Маке (подтвержденное минус
// траты после момента), и валюта операции по счету, как у fin add.
func TestAccountsThroughTheAPI(t *testing.T) {
	h, tok, s := financeTenant(t)
	put := func(name, body string) *httptest.ResponseRecorder {
		return do(h, http.MethodPut, "/api/finances/accounts/"+url.PathEscape(name)+"/balance", body, tok)
	}
	if rec := put("Сбербанк", `{"amount":"1000","create":true,"tz":"Asia/Yekaterinburg"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("Wise", `{"amount":"10","create":true,"currency":"USD","rate":"84,28","tz":"Asia/Yekaterinburg"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create foreign: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("сбербанк", `{"amount":"1000","create":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("create twice: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("Тинькофф", `{"amount":"1"}`); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Сбербанк") {
		t.Fatalf("unknown account: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("Сбербанк", `{"amount":"abc"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad amount: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put("Сбербанк", `{"amount":"1","tz":"Mars/Olympus"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad zone: %d %s", rec.Code, rec.Body.String())
	}
	// Трата после подтверждения уменьшает расчетный остаток.
	if rec := do(h, http.MethodPost, "/api/finances/transactions",
		`{"kind":"expense","amount":"418","category":"Транспорт","account":"Сбербанк","tz":"Asia/Yekaterinburg"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("expense: %d %s", rec.Code, rec.Body.String())
	}
	// Без валюты в запросе операция с валютного счета — в его валюте.
	if rec := do(h, http.MethodPost, "/api/finances/transactions",
		`{"kind":"expense","amount":"2","category":"Сервисы","account":"Wise","tz":"Asia/Yekaterinburg"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("foreign expense: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(h, http.MethodGet, "/api/finances", "", tok)
	var out struct {
		Transactions []struct {
			Account, Currency string
		} `json:"transactions"`
		Accounts []struct {
			Bank, Balance, Current, Currency string
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%.300s)", err, rec.Body.String())
	}
	got := map[string][2]string{}
	for _, a := range out.Accounts {
		got[a.Bank] = [2]string{a.Balance, a.Current}
	}
	if got["Сбербанк"] != [2]string{"1000.00", "582.00"} {
		t.Errorf("Сбербанк %v, want confirmed 1000.00 and current 582.00 (%s)", got["Сбербанк"], rec.Body.String())
	}
	if len(out.Accounts) != 2 {
		t.Errorf("%d accounts, want 2", len(out.Accounts))
	}
	for _, tx := range out.Transactions {
		if tx.Account == "Wise" && tx.Currency != "USD" {
			t.Errorf("an expense from Wise recorded in %q, want USD", tx.Currency)
		}
	}
	if _, err := s.Get(context.Background(), "users/anna/fin/accounts.json"); err != nil {
		t.Errorf("accounts are not in the store: %v", err)
	}
}
