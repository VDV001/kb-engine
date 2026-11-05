package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync`
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

const writableCatalog = `{"entries":[
	{"id":1,"habr_id":1,"title":"Кеширование","url":"https://h/1","category":"golang","status":"keep"}
],"meta":{"categories":{"golang":"Go"}}}`

func storeServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "kb/_data/catalog.json", []byte(writableCatalog), ""); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	src, err := storeCatalogSource(ctx, s, "kb/_data/catalog.json", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	h, err := buildServeHandlerFrom(src, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func total(t *testing.T, h http.Handler, token string) int {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/stats", "", token)
	var st struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("stats: %v (%s)", err, rec.Body.String())
	}
	return st.Total
}

// Запись через API идет той же дорогой, что kbengine add: планировщик ingest,
// дедуп по адресу, проверка категории.
func TestAddThroughTheAPI(t *testing.T) {
	h := storeServer(t)
	rec := do(h, http.MethodPost, "/api/entries", `{"title":"Шардирование","category":"golang","url":"https://h/2","verdict":"keep"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	if got := total(t, h, ""); got != 2 {
		t.Fatalf("total after add = %d, want 2", got)
	}
	again := do(h, http.MethodPost, "/api/entries", `{"title":"Шардирование","category":"golang","url":"https://h/2","verdict":"keep"}`, "")
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"already":true`) {
		t.Fatalf("repeat POST: %d %s", again.Code, again.Body.String())
	}
	bad := do(h, http.MethodPost, "/api/entries", `{"title":"X","category":"nosuch","url":"https://h/3","verdict":"keep"}`, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("undeclared category: %d %s", bad.Code, bad.Body.String())
	}
}

// Одновременные add не получают один номер и не теряют друг друга: при
// конфликте план строится заново по свежему каталогу.
func TestConcurrentAddsGetDistinctIDs(t *testing.T) {
	h := storeServer(t)
	const n = 6
	ids := make([]string, n)
	var wg gosync.WaitGroup
	for i := range n {
		wg.Go(func() {
			rec := do(h, http.MethodPost, "/api/entries", fmt.Sprintf(`{"title":"T%d","category":"golang","url":"https://h/c%d","verdict":"keep"}`, i, i), "")
			if rec.Code != http.StatusCreated {
				t.Errorf("POST %d: %d %s", i, rec.Code, rec.Body.String())
				return
			}
			var out struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			ids[i] = fmt.Sprint(out.ID)
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("two adds got id %s: %v", id, ids)
		}
		seen[id] = true
	}
	if got := total(t, h, ""); got != 1+n {
		t.Fatalf("total = %d, want %d", got, 1+n)
	}
}

func TestPatchThroughTheAPI(t *testing.T) {
	h := storeServer(t)
	if rec := do(h, http.MethodPatch, "/api/entries/1", `{"verdict":"consider"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(h, http.MethodGet, "/api/entries", "", "")
	if !strings.Contains(rec.Body.String(), "consider") {
		t.Fatalf("entry after PATCH: %s", rec.Body.String())
	}
	if rec := do(h, http.MethodPatch, "/api/entries/99", `{"verdict":"keep"}`, ""); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH of a missing id: %d", rec.Code)
	}
}

// Сервер с диска только читает: пишет туда CLI на том же Маке.
func TestDiskServeHasNoWriteRoutes(t *testing.T) {
	path := writeCatalog(t, writableCatalog)
	h, err := buildServeHandler(path, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/9"}`, ""); rec.Code < 400 {
		t.Fatalf("POST on a disk serve: %d", rec.Code)
	}
}

// С токеном Б запись идет в базу Б и не трогает базу А.
func TestTenantWritesStayInTheirBase(t *testing.T) {
	f := newTenants(t)
	if rec := do(f.h, http.MethodPost, "/api/entries", `{"title":"Только Б","category":"golang","url":"https://h/b-only","verdict":"keep"}`, f.tokB); rec.Code != http.StatusCreated {
		t.Fatalf("B POST: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"verdict":"consider"}`, f.tokB); rec.Code != http.StatusOK {
		t.Fatalf("B PATCH: %d %s", rec.Code, rec.Body.String())
	}
	a := do(f.h, http.MethodGet, "/api/entries", "", f.tokA).Body.String()
	if strings.Contains(a, "Только Б") || strings.Contains(a, "consider") {
		t.Fatalf("B's writes reached A's base: %s", a)
	}
	if got := total(t, f.h, f.tokB); got != 3 {
		t.Fatalf("B's total = %d, want 3", got)
	}
	if rec := do(f.h, http.MethodPost, "/api/entries", `{"title":"x","category":"golang","url":"https://h/x"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST without a token: %d", rec.Code)
	}
}

// storeServerWith is storeServer over a catalog given here; returns the store
// too, to look at the object itself.
func storeServerWith(t *testing.T, catalog string) (http.Handler, *blobfs.Store) {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "kb/_data/catalog.json", []byte(catalog), ""); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	src, err := storeCatalogSource(ctx, s, "kb/_data/catalog.json", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	h, err := buildServeHandlerFrom(src, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, s
}

// Правка, которая ничего не меняет, отвечает updated 0 и не пишет новую
// версию каталога.
func TestNoOpPatchWritesNothing(t *testing.T) {
	h, s := storeServerWith(t, writableCatalog)
	before, _ := s.Get(context.Background(), "kb/_data/catalog.json")
	rec := do(h, http.MethodPatch, "/api/entries/1", `{"verdict":"keep"}`, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":0`) {
		t.Fatalf("no-op PATCH: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := s.Get(context.Background(), "kb/_data/catalog.json")
	if after.ETag != before.ETag {
		t.Fatal("a no-op PATCH rewrote the catalog")
	}
}

func TestPatchFieldRules(t *testing.T) {
	h, _ := storeServerWith(t, writableCatalog)
	cases := []struct {
		name, body string
		code       int
		says       string
	}{
		{"empty url clears it", `{"url":""}`, http.StatusOK, `"updated":1`},
		{"empty description is refused, not ignored", `{"description":""}`, http.StatusBadRequest, "description"},
		{"undeclared category", `{"category":"golnag"}`, http.StatusBadRequest, "golnag"},
		{"declared category", `{"category":"golang","verdict":"consider"}`, http.StatusOK, `"updated":1`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(h, http.MethodPatch, "/api/entries/1", c.body, "")
			if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.says) {
				t.Fatalf("%d %s; want %d naming %q", rec.Code, rec.Body.String(), c.code, c.says)
			}
			if strings.Contains(rec.Body.String(), "--lifecycle") {
				t.Errorf("API answer speaks in CLI flags: %s", rec.Body.String())
			}
		})
	}
	if rec := do(h, http.MethodGet, "/api/entries", "", ""); strings.Contains(rec.Body.String(), "https://h/1") {
		t.Errorf("url not cleared: %s", rec.Body.String())
	}
	if rec := do(h, http.MethodPatch, "/api/entries/99", `{"verdict":"keep"}`, ""); rec.Code != http.StatusNotFound {
		t.Errorf("PATCH of a missing id: %d, want 404", rec.Code)
	}
}

func TestPostTagRulesAndUncheckedCategory(t *testing.T) {
	h, _ := storeServerWith(t, writableCatalog)
	if rec := do(h, http.MethodPost, "/api/entries", `{"title":"T","category":"golang","url":"https://h/t","tags":["go, cache"]}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("tag with a comma: %d, want 400", rec.Code)
	}
	bare, _ := storeServerWith(t, `{"entries":[]}`)
	rec := do(bare, http.MethodPost, "/api/entries", `{"title":"T","category":"anything","url":"https://h/t","verdict":"keep"}`, "")
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"category_unchecked":true`) {
		t.Errorf("catalog without a dictionary: %d %s", rec.Code, rec.Body.String())
	}
	again := do(h, http.MethodPost, "/api/entries", `{"title":"other","category":"golang","url":"  https://h/1  ","verdict":"keep"}`, "")
	if !strings.Contains(again.Body.String(), `"id":1`) || !strings.Contains(again.Body.String(), "Кеширование") {
		t.Errorf("repeat with spaces around the url: %s", again.Body.String())
	}
}
