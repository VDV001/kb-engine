package kbserver_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
)

// portraitServer — /api/portrait как у kbengine serve: версия в ETag, запись
// только с If-Match или If-None-Match: *, чужая версия — 412.
func portraitServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var data []byte
	ver := 0
	etag := func() string { return `"v` + strconv.Itoa(ver) + `"` }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(kbserver.TokenHeader) != token || r.URL.Path != "/api/portrait" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if data == nil {
				_, _ = w.Write([]byte(`{"facts":[]}`))
				return
			}
			w.Header().Set("ETag", etag())
			_, _ = w.Write(data)
		case http.MethodPut:
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "json", http.StatusUnsupportedMediaType)
				return
			}
			im, inm := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
			switch {
			case im == "" && inm != "*":
				http.Error(w, "pre", http.StatusPreconditionRequired)
				return
			case inm == "*" && data != nil, im != "" && (data == nil || im != etag()):
				http.Error(w, "changed", http.StatusPreconditionFailed)
				return
			}
			data, _ = io.ReadAll(r.Body)
			ver++
			w.Header().Set("ETag", etag())
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Портрет на kb-сервере: пустой → создать → прочитать; два устройства с
// одной версией — второе получает portrait.ErrConflict, а не затирает.
func TestPortraitStore(t *testing.T) {
	srv := portraitServer(t)
	c := kbserver.Client{URL: srv.URL, Token: token}
	mac, phone := kbserver.NewPortraitStore(c), kbserver.NewPortraitStore(c)
	ctx := t.Context()
	p, err := mac.Load(ctx)
	if err != nil || len(p.Facts()) != 0 {
		t.Fatalf("пустой: %v %v", p.Facts(), err)
	}
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	f, _ := domain.NewFact("f1", domain.SectionWork, "Пишет на Go", "вручную", domain.FactApproved, at)
	if p, err = p.Add(f); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mac.Save(ctx, p); err != nil {
		t.Fatalf("создание: %v", err)
	}
	if err := phone.Save(ctx, p); !errors.Is(err, portrait.ErrConflict) {
		t.Fatalf("второе устройство по старой версии = %v", err)
	}
	got, err := phone.Load(ctx)
	if err != nil || !strings.Contains(domain.PortraitText(got.Facts()), "Пишет на Go") {
		t.Fatalf("прочитано: %v %v", got.Facts(), err)
	}
	g, _ := domain.NewFact("f2", domain.SectionPrefs, "Отвечать коротко", "вручную", domain.FactApproved, at)
	got, _ = got.Add(g)
	if err := phone.Save(ctx, got); err != nil {
		t.Fatalf("правка от свежей версии: %v", err)
	}
	if _, err := kbserver.NewPortraitStore(kbserver.Client{URL: srv.URL, Token: "wrong"}).Load(ctx); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Fatalf("чужой токен = %v", err)
	}
}
