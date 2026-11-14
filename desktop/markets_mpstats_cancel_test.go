package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Новая загрузка ниш отменяет прежнюю (ревью 14.11, №7): окно отбрасывает
// старый ответ, а запросы MPStats шли бы до тайм-аута и тратили тариф.
func TestMarketsNichesMPStats_newLoadCancelsOld(t *testing.T) {
	s, _, _ := newServerSettings(t)
	seenA := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case r.URL.Path == "/api/oz/get/category/subcategories" && r.URL.Query().Get("path") == "A":
			select {
			case seenA <- struct{}{}:
			default:
			}
			<-r.Context().Done() // висит, пока клиент не отменит
		case r.URL.Path == "/api/oz/get/category/subcategories":
			_, _ = w.Write([]byte(`[{"name":"Брюки","sales":150,"revenue":3000,"sellers_with_sells":7}]`))
		case r.URL.Path == "/api/oz/get/category/sellers":
			_, _ = w.Write([]byte(`[{"revenue":1500}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	a := newApp(nil, s, nil)
	a.ctx = context.Background()
	done := make(chan error, 1)
	go func() {
		_, err := a.MarketsNichesMPStats("A", "2026-10-15", "2026-11-13")
		done <- err
	}()
	select {
	case <-seenA:
	case <-time.After(5 * time.Second):
		t.Fatal("the first load never reached MPStats")
	}
	if _, err := a.MarketsNichesMPStats("B", "2026-10-15", "2026-11-13"); err != nil {
		t.Fatalf("second load: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the cancelled load returned a table")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first load still runs after the second one started")
	}
}
