package ghrelease_test

import (
	"errors"
	"io"
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
	// Окно без https не ходит, даже если адрес выпуска задан с http.
	if _, err := (ghrelease.Client{Base: plain.URL + "/", HTTP: plain.Client()}).Fetch(t.Context(), "x", 10); err == nil {
		t.Error("plain http base fetched")
	}
	// Своя политика переадресаций вызывающего остается в силе.
	own := errors.New("caller refused")
	srv2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r/hop" {
			http.Redirect(w, r, "/r/exact", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv2.Close()
	strict := *srv2.Client()
	strict.CheckRedirect = func(*http.Request, []*http.Request) error { return own }
	if _, err := (ghrelease.Client{Base: srv2.URL + "/r/", HTTP: &strict}).Fetch(t.Context(), "hop", 10); !errors.Is(err, own) {
		t.Errorf("caller's redirect policy dropped: %v", err)
	}
	for _, p := range []string{"../secrets", "/etc/passwd", "a/../../b", "https://evil/x"} {
		if _, err := c.Fetch(t.Context(), p, 10); !errors.Is(err, ghrelease.ErrBadPath) {
			t.Errorf("path %q: %v", p, err)
		}
	}
}

// Open streams the file and cuts it at the limit (ревью #672: сборка — потоком).
func TestOpenStreamsWithinTheLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 20)))
	}))
	defer srv.Close()
	c := ghrelease.Client{Base: srv.URL + "/r/", HTTP: srv.Client()}
	rc, err := c.Open(t.Context(), "big", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if !errors.Is(err, ghrelease.ErrTooLarge) || len(b) > 10 {
		t.Errorf("read %d bytes, %v — want ErrTooLarge past 10", len(b), err)
	}
	if _, err := c.Open(t.Context(), "../x", 10); !errors.Is(err, ghrelease.ErrBadPath) {
		t.Errorf("bad path: %v", err)
	}
}
