package monium_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/monium"
)

var fixedNow = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

type payload struct {
	ResourceLogs []struct {
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano   string
				SeverityText   string
				SeverityNumber int
				Body           struct{ StringValue string }
			}
		}
	}
}

type received struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

func (r *received) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs, r.body = append(r.reqs, req), append(r.body, b)
		r.mu.Unlock()
		w.WriteHeader(status)
	}
}

func (r *received) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func config(url string) monium.Config {
	return monium.Config{
		Endpoint: url + "/otlp/v1/logs", APIKey: "test-key", Project: "folder__test",
		Cluster: "production", Service: "kb-server", Now: func() time.Time { return fixedNow },
	}
}

// run writes lines, stops the sender and waits until it has sent what is left.
func run(t *testing.T, cfg monium.Config, lines ...string) *monium.Writer {
	t.Helper()
	w, err := monium.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	for _, l := range lines {
		if n, err := w.Write([]byte(l)); n != len(l) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}
	return w
}

func records(t *testing.T, r *received) []string {
	t.Helper()
	var out []string
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.body {
		var p payload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("body is not OTLP JSON: %v: %s", err, b)
		}
		for _, rl := range p.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					out = append(out, lr.Body.StringValue)
				}
			}
		}
	}
	return out
}

func TestWriterPostsErrorAsOTLPJSON(t *testing.T) {
	var got received
	srv := httptest.NewServer(got.handler(200))
	defer srv.Close()
	run(t, config(srv.URL), "portrait read: boom\n")

	if got.count() != 1 {
		t.Fatalf("requests = %d, want 1", got.count())
	}
	req := got.reqs[0]
	if req.Method != http.MethodPost || req.URL.Path != "/otlp/v1/logs" {
		t.Errorf("%s %s", req.Method, req.URL.Path)
	}
	for k, want := range map[string]string{
		"Authorization": "Api-Key test-key", "X-Monium-Project": "folder__test",
		"X-Monium-Cluster": "production", "X-Monium-Service": "kb-server", "Content-Type": "application/json",
	} {
		if v := req.Header.Get(k); v != want {
			t.Errorf("header %s = %q, want %q", k, v, want)
		}
	}
	var p payload
	if err := json.Unmarshal(got.body[0], &p); err != nil {
		t.Fatal(err)
	}
	lr := p.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if lr.Body.StringValue != "portrait read: boom" {
		t.Errorf("body %q: the newline must go", lr.Body.StringValue)
	}
	if lr.SeverityText != "ERROR" || lr.SeverityNumber != 17 {
		t.Errorf("severity %q %d", lr.SeverityText, lr.SeverityNumber)
	}
	if want := strconv.FormatInt(fixedNow.UnixNano(), 10); lr.TimeUnixNano != want {
		t.Errorf("time %q, want %q", lr.TimeUnixNano, want)
	}
}

func TestWriterKeepsOrderAcrossLines(t *testing.T) {
	var got received
	srv := httptest.NewServer(got.handler(200))
	defer srv.Close()
	run(t, config(srv.URL), "first\n", "second\n", "third\n")
	if g := strings.Join(records(t, &got), "|"); g != "first|second|third" {
		t.Errorf("records %q", g)
	}
}

// Секрет не должен уйти в журнал, даже если попал в текст ошибки (ревью безопасности).
func TestWriterRedactsSecrets(t *testing.T) {
	tests := []struct {
		name, line, secret string
	}{
		{"bearer", "call failed: Authorization: Bearer abc123SECRET\n", "abc123SECRET"},
		{"api key", "call failed: Api-Key AQVNsecretkey\n", "AQVNsecretkey"},
		{"token in url", "get https://x.example/?token=tok123&a=1: boom\n", "tok123"},
		{"plain stays", "portrait read: boom\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got received
			srv := httptest.NewServer(got.handler(200))
			defer srv.Close()
			run(t, config(srv.URL), tc.line)
			recs := records(t, &got)
			if len(recs) != 1 {
				t.Fatalf("records = %d, want 1", len(recs))
			}
			if tc.secret == "" {
				if recs[0] != strings.TrimSpace(tc.line) {
					t.Errorf("plain line changed: %q", recs[0])
				}
				return
			}
			if strings.Contains(recs[0], tc.secret) {
				t.Errorf("secret left in %q", recs[0])
			}
		})
	}
}

func TestWriterNeverBlocksWhenMoniumHangs(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	cfg := config(srv.URL)
	cfg.Queue = 8
	w, err := monium.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go w.Run(ctx)

	start := time.Now()
	for range 10000 {
		if n, err := w.Write([]byte("boom\n")); n != 5 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("10000 writes took %v: the writer waits for Monium", d)
	}
	if w.Dropped() == 0 {
		t.Error("nothing dropped with a queue of 8 and a hanging server")
	}
}

func TestWriterSurvivesMoniumErrors(t *testing.T) {
	var got received
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got.handler(503)(w, r)
	}))
	defer srv.Close()
	run(t, config(srv.URL), "boom\n")
	if calls.Load() == 0 {
		t.Error("no attempt to send: the 503 path was not exercised")
	}
}

func TestNewRefusesIncompleteConfig(t *testing.T) {
	tests := []struct {
		name string
		edit func(*monium.Config)
	}{
		{"no endpoint", func(c *monium.Config) { c.Endpoint = "" }},
		{"no key", func(c *monium.Config) { c.APIKey = "" }},
		{"no project", func(c *monium.Config) { c.Project = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config("http://127.0.0.1:1")
			tc.edit(&cfg)
			if _, err := monium.New(cfg); !errors.Is(err, monium.ErrConfig) {
				t.Errorf("err = %v, want ErrConfig", err)
			}
		})
	}
}
