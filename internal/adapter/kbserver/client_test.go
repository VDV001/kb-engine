package kbserver_test

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/kbserver"
)

const token = "k3Jx9_secret-token-0123456789abcdefXYZ"

// fakeKB — kbengine serve в миниатюре: /api/engine и /api/stats за Bearer,
// по HTTPS, как его поднимает serve --token-file --tls-cert.
func fakeKB(t *testing.T) *httptest.Server {
	t.Helper()
	want := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/engine", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.29.0","commit":"abc"}`))
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total":2431,"by_kind":{"article":2000}}`))
	})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_Info(t *testing.T) {
	srv := fakeKB(t)
	c := kbserver.Client{URL: srv.URL + "/", Token: token, HTTP: srv.Client()}
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.29.0" || info.Entries != 2431 {
		t.Fatalf("info = %+v", info)
	}
}

func TestClient_InfoErrors(t *testing.T) {
	srv := fakeKB(t)
	notKB := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>это сайт, а не kb</html>"))
	}))
	t.Cleanup(notKB.Close)
	down := httptest.NewTLSServer(http.NotFoundHandler())
	downURL, downClient := down.URL, down.Client()
	down.Close()
	tests := []struct {
		name string
		c    kbserver.Client
		want error
	}{
		{"без токена", kbserver.Client{URL: srv.URL, HTTP: srv.Client()}, kbserver.ErrUnauthorized},
		{"чужой токен", kbserver.Client{URL: srv.URL, Token: strings.Repeat("x", 40), HTTP: srv.Client()}, kbserver.ErrUnauthorized},
		{"сервер выключен", kbserver.Client{URL: downURL, Token: token, HTTP: downClient}, kbserver.ErrUnreachable},
		{"не kb-сервер", kbserver.Client{URL: notKB.URL, Token: token, HTTP: notKB.Client()}, kbserver.ErrNotKB},
		// Сертификату, которому система не доверяет, — отказ, а не тихое
		// согласие: иначе токен ушел бы тому, кто встал посередине.
		{"чужой сертификат", kbserver.Client{URL: srv.URL, Token: token, HTTP: &http.Client{}}, kbserver.ErrUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.c.Info(context.Background()); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Сервер, который не отвечает, не вешает окно: ответ ограничен контекстом.
func TestClient_InfoRespectsContext(t *testing.T) {
	slow := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := kbserver.Client{URL: slow.URL, Token: token, HTTP: slow.Client()}.Info(ctx)
	if !errors.Is(err, kbserver.ErrUnreachable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v за %v", err, time.Since(start))
	}
}

// Редирект не выполняется: Go пересылает Authorization на тот же хост и при
// смене https на http — токен ушел бы открытым текстом мимо правила «http
// только на этот Mac». Цель редиректа не получает ни одного запроса.
func TestClient_RefusesRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(target.Close)
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(redir.Close)
	if _, err := (kbserver.Client{URL: redir.URL, Token: token, HTTP: redir.Client()}).Info(context.Background()); err == nil {
		t.Fatal("редирект принят")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("клиент пошел по редиректу: %d запросов к цели", n)
	}
}

// Коды ответа различаются: настоящий kb-сервер с ошибкой (5xx, 429) — не «не
// kb-сервер», а «сервер ответил ошибкой»; 403 — вопрос доступа, как 401.
func TestClient_StatusMapping(t *testing.T) {
	for code, want := range map[int]error{
		http.StatusForbidden:           kbserver.ErrUnauthorized,
		http.StatusTooManyRequests:     kbserver.ErrServerError,
		http.StatusInternalServerError: kbserver.ErrServerError,
		http.StatusBadGateway:          kbserver.ErrServerError,
		http.StatusServiceUnavailable:  kbserver.ErrServerError,
		http.StatusNotFound:            kbserver.ErrNotKB,
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		_, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Info(context.Background())
		srv.Close()
		if !errors.Is(err, want) {
			t.Errorf("%d: err = %v, want %v", code, err, want)
		}
	}
}
