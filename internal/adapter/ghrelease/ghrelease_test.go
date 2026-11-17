package ghrelease_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/ghrelease"
)

// GitHub отдает файл выпуска переадресацией на свое хранилище — по https.
func TestFetchFollowsHTTPSRedirect(t *testing.T) {
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("manifest"))
	}))
	defer storage.Close()
	var asked string
	github := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		http.Redirect(w, r, storage.URL+"/blob", http.StatusFound)
	}))
	defer github.Close()
	c := ghrelease.Client{Base: github.URL + "/VDV001/kb-engine/releases/", HTTP: github.Client()}
	b, err := c.Fetch(t.Context(), "latest/download/dvk-manifest.json", 100)
	if err != nil || string(b) != "manifest" {
		t.Fatalf("%q, %v", b, err)
	}
	if asked != "/VDV001/kb-engine/releases/latest/download/dvk-manifest.json" {
		t.Errorf("asked %s", asked)
	}
}

func TestFetchRefusals(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r/big":
			_, _ = w.Write([]byte(strings.Repeat("a", 11)))
		case "/r/exact":
			_, _ = w.Write([]byte(strings.Repeat("a", 10)))
		case "/r/to-http":
			http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := ghrelease.Client{Base: srv.URL + "/r/", HTTP: srv.Client()}
	if b, err := c.Fetch(t.Context(), "exact", 10); err != nil || len(b) != 10 {
		t.Errorf("exactly the limit: %d, %v", len(b), err)
	}
	if _, err := c.Fetch(t.Context(), "big", 10); !errors.Is(err, ghrelease.ErrTooLarge) {
		t.Errorf("over the limit: %v", err)
	}
	if _, err := c.Fetch(t.Context(), "missing", 10); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
	if _, err := c.Fetch(t.Context(), "to-http", 10); err == nil {
		t.Error("redirect to plain http followed")
	}
	for _, p := range []string{"../secrets", "/etc/passwd", "a/../../b", "https://evil/x"} {
		if _, err := c.Fetch(t.Context(), p, 10); !errors.Is(err, ghrelease.ErrBadPath) {
			t.Errorf("path %q: %v", p, err)
		}
	}
}
