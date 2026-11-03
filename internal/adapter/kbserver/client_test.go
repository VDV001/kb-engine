package kbserver_test

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
