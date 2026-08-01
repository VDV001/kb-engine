package linkcheck_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/linkcheck"
)

func TestHead_returnsStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	resp, err := linkcheck.New(2*time.Second, 0).Head(srv.URL)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if resp.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", resp.Code)
	}
}

// A redirect must be reported as a redirect. Following it would answer 200 for
// a URL the catalog no longer names correctly — the entry would look healthy
// while its stored address is stale.
func TestHead_doesNotFollowRedirects(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusMovedPermanently)
	}))
	defer moved.Close()

	resp, err := linkcheck.New(2*time.Second, 0).Head(moved.URL)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if resp.Code != http.StatusMovedPermanently {
		t.Fatalf("code = %d, want 301", resp.Code)
	}
	// The target is the point: the catalog stores addresses, and a moved one
	// has to be knowable without a second request.
	if resp.Location != final.URL {
		t.Fatalf("Location = %q, want %q", resp.Location, final.URL)
	}
}

func TestHead_sendsAUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := linkcheck.New(2*time.Second, 0).Head(srv.URL); err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got == "" {
		t.Fatal("no User-Agent sent — many hosts answer 403 to the default client, and that 403 would be recorded as a fact about the article")
	}
}

// The delay must apply between requests, or a scan of 1313 urls hits one host
// at full speed and collects the 403s it caused itself.
func TestHead_pausesBetweenRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := linkcheck.New(2*time.Second, 80*time.Millisecond)
	start := time.Now()
	for range 3 {
		if _, err := c.Head(srv.URL); err != nil {
			t.Fatalf("Head: %v", err)
		}
	}
	// Two gaps between three requests.
	if elapsed := time.Since(start); elapsed < 160*time.Millisecond {
		t.Fatalf("three requests took %v, want at least two 80ms gaps", elapsed)
	}
}
