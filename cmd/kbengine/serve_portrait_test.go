package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const portraitDoc = `{"facts":[{"id":"f1","section":"work","text":"PORTRAIT-ALPHA пишет на Go","status":"approved","at":"2026-11-07T10:00:00Z"}]}`

func portraitPut(h http.Handler, body, token string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/portrait", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Портрет «Что DVK помнит обо мне» (#479) на kb-сервере: у каждого свой,
// версия — ETag, запись только от прочитанной версии.
func TestTenantsPortrait(t *testing.T) {
	f := newTenants(t)
	rec := get(f.h, "/api/portrait", f.tokA)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"facts"`) || rec.Header().Get("ETag") != "" {
		t.Fatalf("пустой портрет: %d %q etag %q", rec.Code, rec.Body.String(), rec.Header().Get("ETag"))
	}
	if rec := portraitPut(f.h, portraitDoc, f.tokA); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("без If-Match/If-None-Match = %d", rec.Code)
	}
	put := portraitPut(f.h, portraitDoc, f.tokA, "If-None-Match", "*")
	etag := put.Header().Get("ETag")
	if put.Code != http.StatusNoContent || etag == "" {
		t.Fatalf("создание = %d etag %q (%s)", put.Code, etag, put.Body.String())
	}
	rec = get(f.h, "/api/portrait", f.tokA)
	if !strings.Contains(rec.Body.String(), "PORTRAIT-ALPHA") || rec.Header().Get("ETag") != etag {
		t.Fatalf("прочитано: %q etag %q, ждали %q", rec.Body.String(), rec.Header().Get("ETag"), etag)
	}
	if rec := get(f.h, "/api/portrait", f.tokB); strings.Contains(rec.Body.String(), "PORTRAIT-ALPHA") {
		t.Fatalf("Б видит портрет А: %s", rec.Body.String())
	}
	if rec := portraitPut(f.h, portraitDoc, f.tokA, "If-None-Match", "*"); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("второе создание = %d", rec.Code)
	}
	if rec := portraitPut(f.h, portraitDoc, f.tokA, "If-Match", `"stale"`); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("старая версия = %d", rec.Code)
	}
	if rec := portraitPut(f.h, `{"facts":[{"id":"x","section":"чужой","text":"t","status":"approved"}]}`, f.tokA, "If-Match", etag); rec.Code != http.StatusBadRequest {
		t.Fatalf("не портрет = %d", rec.Code)
	}
	next := strings.Replace(portraitDoc, "пишет на Go", "пишет на Go и Rust", 1)
	if rec := portraitPut(f.h, next, f.tokA, "If-Match", etag); rec.Code != http.StatusNoContent || rec.Header().Get("ETag") == etag {
		t.Fatalf("правка = %d etag %q", rec.Code, rec.Header().Get("ETag"))
	}
	req := httptest.NewRequest(http.MethodPut, "/api/portrait", strings.NewReader(next))
	req.Header.Set("Authorization", "Bearer "+f.tokA)
	req.Header.Set("If-Match", etag)
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("без JSON = %d", rec.Code)
	}
}
