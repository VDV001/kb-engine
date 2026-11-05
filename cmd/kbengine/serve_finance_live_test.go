package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync`
	"sync/atomic"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// TestLiveFinancesInYandex runs financesScenario over a real Object Storage
// bucket. Нужны KB_BLOB_LIVE_BUCKET и
// KB_BLOB_LIVE_TOKEN_FILE (IAM-токен `yc iam create-token` в файле 0600), как у
// TestLiveYandex. Все ключи — под test/<случайное>/, их чистит правило
// жизненного цикла бакета.
func TestLiveFinancesInYandex(t *testing.T) {
	bkt, tokFile := os.Getenv("KB_BLOB_LIVE_BUCKET"), os.Getenv("KB_BLOB_LIVE_TOKEN_FILE")
	if bkt == "" || tokFile == "" {
		t.Skip("KB_BLOB_LIVE_BUCKET / KB_BLOB_LIVE_TOKEN_FILE not set")
	}
	raw, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatal(err)
	}
	s, err := blobs3.New(blobs3.Config{Endpoint: blobs3.YandexEndpoint, Bucket: bkt,
		Token: blobs3.StaticToken(strings.TrimSpace(string(raw))), Prefix: "test/" + rand.Text()[:12] + "/"})
	if err != nil {
		t.Fatal(err)
	}
	// Сбои Yandex на его стороне (500 на одновременной условной записи)
	// считаются: зеленый прогон без единого сбоя ничего не говорит о повторах.
	counted := &countingFailures{Store: s}
	financesScenario(t, counted)
	t.Logf("store failures retried: %d", counted.n.Load())
}

// countingFailures counts the ErrTransient answers the store gave.
type countingFailures struct {
	blob.Store
	n atomic.Int64
}

func (c *countingFailures) Get(ctx context.Context, key string) (blob.Object, error) {
	o, err := c.Store.Get(ctx, key)
	if errors.Is(err, blob.ErrTransient) {
		c.n.Add(1)
	}
	return o, err
}

func (c *countingFailures) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	etag, err := c.Store.Put(ctx, key, data, ifMatch)
	if errors.Is(err, blob.ErrTransient) {
		c.n.Add(1)
	}
	return etag, err
}

// Счета, подтверждение, траты и расчетный остаток при одновременных записях,
// через сервер людей. На диске — каждый прогон, в бакете — TestLiveFinancesInYandex.
func TestFinancesScenarioOnDisk(t *testing.T) {
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	financesScenario(t, s)
}

func financesScenario(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := context.Background()
	uid, err := domain.NewUserID("anna")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := userauth.Add(ctx, s, uid)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	h := tenantsHandler(ctx, s, &stderr)
	put := func(name, body string) int {
		return do(h, http.MethodPut, "/api/finances/accounts/"+url.PathEscape(name)+"/balance", body, tok).Code
	}

	// Четыре счета заводятся одновременно — условная запись не теряет ни один.
	var wg gosync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			if code := put(fmt.Sprintf("Счет %d", i), `{"amount":"1000","create":true,"tz":"Asia/Yekaterinburg"}`); code != http.StatusCreated {
				t.Errorf("create Счет %d: %d", i, code)
			}
		})
	}
	wg.Wait()
	// Одновременно: три траты со счета 0 и подтверждение счета 1.
	for i := range 3 {
		wg.Go(func() {
			rec := do(h, http.MethodPost, "/api/finances/transactions",
				fmt.Sprintf(`{"kind":"expense","amount":"%d","category":"Еда","account":"Счет 0","tz":"Asia/Yekaterinburg"}`, 100+i), tok)
			if rec.Code != http.StatusCreated {
				t.Errorf("expense %d: %d %s", i, rec.Code, rec.Body.String())
			}
		})
	}
	wg.Go(func() {
		if code := put("счет 1", `{"amount":"777,50","tz":"Asia/Yekaterinburg"}`); code != http.StatusOK {
			t.Errorf("confirm Счет 1: %d", code)
		}
	})
	wg.Wait()

	rec := do(h, http.MethodGet, "/api/finances", "", tok)
	var out struct {
		Transactions []json.RawMessage                         `json:"transactions"`
		Accounts     []struct{ Bank, Balance, Current string } `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%.300s)", err, rec.Body.String())
	}
	got := map[string][2]string{}
	for _, a := range out.Accounts {
		got[a.Bank] = [2]string{a.Balance, a.Current}
	}
	want := map[string][2]string{
		"Счет 0": {"1000.00", "697.00"}, // 1000 − 100 − 101 − 102
		"Счет 1": {"777.50", "777.50"},
		"Счет 2": {"1000.00", "1000.00"},
		"Счет 3": {"1000.00", "1000.00"},
	}
	if len(got) != len(want) || len(out.Transactions) != 3 {
		t.Fatalf("%d accounts, %d transactions: %s", len(got), len(out.Transactions), rec.Body.String())
	}
	for bank, w := range want {
		if got[bank] != w {
			t.Errorf("%s: %v, want %v", bank, got[bank], w)
		}
	}
	t.Logf("scenario: %d accounts, %d transactions, balances %v", len(got), len(out.Transactions), got)
}
