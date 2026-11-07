package modelprices_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/modelprices"
)

const listing = `{"data":[
 {"id":"anthropic/claude-haiku-5.5","pricing":{"input":"0.0000001","output":"0.0000005","input_tiers":[{"cost":"0.0000005","min":100001}]}},
 {"id":"openai/no-price"},
 {"id":"bad/price","pricing":{"input":"дешево","output":"0"}}
]}`

func server(t *testing.T, hits *int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

// Цены берутся из списка моделей шлюза раз в сутки и лежат в кэше (0600):
// модель без цены или с испорченной ценой — просто без цены, а не ошибка всего списка.
func TestCache_fetchesOnceADay(t *testing.T) {
	hits := 0
	s := server(t, &hits, listing)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := modelprices.Cache{Path: filepath.Join(t.TempDir(), "prices.json"), BaseURL: s.URL + "/v1", Now: func() time.Time { return now }}
	book, err := c.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, ok := book["anthropic/claude-haiku-5.5"]
	if in, out := p.PerMillion(); !ok || in != "0.1" || out != "0.5" {
		t.Fatalf("haiku: %v %q %q", ok, in, out)
	}
	if _, ok := book["openai/no-price"]; ok {
		t.Fatal("модель без цены получила цену")
	}
	if _, ok := book["bad/price"]; ok {
		t.Fatal("испорченная цена принята")
	}
	if st, err := os.Stat(c.Path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("кэш: %v %v", st, err)
	}
	now = now.Add(23 * time.Hour)
	if _, err := c.Load(context.Background()); err != nil || hits != 1 {
		t.Fatalf("через 23 ч: hits=%d err=%v", hits, err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := c.Load(context.Background()); err != nil || hits != 2 {
		t.Fatalf("через 25 ч: hits=%d err=%v", hits, err)
	}
}

// Шлюз недоступен — старый кэш служит дальше, ошибка сказана; кэша нет — пусто и ошибка.
func TestCache_staleWhenGatewayDown(t *testing.T) {
	hits := 0
	s := server(t, &hits, listing)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := modelprices.Cache{Path: filepath.Join(t.TempDir(), "prices.json"), BaseURL: s.URL + "/v1", Now: func() time.Time { return now }}
	if _, err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	now = now.Add(48 * time.Hour)
	book, err := c.Load(context.Background())
	if err == nil || len(book) != 1 {
		t.Fatalf("старый кэш: %d цен, err=%v", len(book), err)
	}
	empty := modelprices.Cache{Path: filepath.Join(t.TempDir(), "none.json"), BaseURL: s.URL + "/v1", Now: func() time.Time { return now }}
	if book, err := empty.Load(context.Background()); err == nil || len(book) != 0 {
		t.Fatalf("без кэша: %d цен, err=%v", len(book), err)
	}
}

// Шлюз не ответил — следующие 10 минут в сеть не ходим: без сети «Деньги»
// иначе висели бы по сроку запроса на каждом открытии.
func TestCache_backsOffAfterFailure(t *testing.T) {
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer s.Close()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := &modelprices.Cache{Path: filepath.Join(t.TempDir(), "prices.json"), BaseURL: s.URL + "/v1", Now: func() time.Time { return now }}
	for range 3 {
		if _, err := c.Load(context.Background()); err == nil {
			t.Fatal("отказ шлюза без ошибки")
		}
	}
	if hits != 1 {
		t.Fatalf("за минуту в шлюз ходили %d раз", hits)
	}
	now = now.Add(11 * time.Minute)
	_, _ = c.Load(context.Background())
	if hits != 2 {
		t.Fatalf("через 11 минут hits=%d", hits)
	}
}
