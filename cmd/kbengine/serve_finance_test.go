package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync`
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
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
