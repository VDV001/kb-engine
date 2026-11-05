package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync`
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// financeTenant registers one person and returns the server, the token and the
// store.
func financeTenant(t *testing.T) (http.Handler, string, *blobfs.Store) {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
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
	if _, err := s.Put(ctx, "users/anna/fin/aliases.json", []byte(`{"places":{"пятерочка":{"place":"Пятёрочка","category":"Еда"}}}`), ""); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	return tenantsHandler(ctx, s, &stderr), tok, s
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
