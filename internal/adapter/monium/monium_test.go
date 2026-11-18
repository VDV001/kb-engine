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
// Формы взяты из ревью #696: Go-ошибки печатают URL, заголовки и JSON как есть.
func TestWriterRedactsSecrets(t *testing.T) {
	tests := []struct {
		name, line string
		secrets    []string // пусто — строка должна дойти без изменений
	}{
		{"bearer", "call failed: Authorization: Bearer abc123SECRET\n", []string{"abc123SECRET"}},
		{"bare bearer", "call failed: got Bearer abc123SECRET back\n", []string{"abc123SECRET"}},
		{"api key", "call failed: Api-Key AQVNsecretkey\n", []string{"AQVNsecretkey"}},
		{"basic auth", "call failed: Authorization: Basic dXNlcjpwYXNz\n", []string{"dXNlcjpwYXNz"}},
		{"header with colon", "denied x-api-key: AQVNsecret\n", []string{"AQVNsecret"}},
		{"token in url", "get https://x.example/?token=tok123&a=1: boom\n", []string{"tok123"}},
		{"json body", "body {\"token\":\"abc123\",\"api_key\":\"zzz987\"}\n", []string{"abc123", "zzz987"}},
		{"underscored names", "s3: access_key=AKIA123 secret_key=abcdef12\n", []string{"AKIA123", "abcdef12"}},
		{"password with colon", "login failed password: hunter2\n", []string{"hunter2"}},
		{"credentials in url", "dial https://user:p4ssw0rd@host/x: boom\n", []string{"p4ssw0rd"}},
		{"quoted value with a space", "bad config key=\"abc def\" here\n", []string{"abc", "def"}},
		{"cookie session", "cookie: session=abc123xyz\n", []string{"abc123xyz"}},
		{"api-key scheme in authorization", "Authorization: Api-Key AQVN123456789\n", []string{"AQVN123456789"}},
		{"signed url", "get https://b.s3.example/k?X-Amz-Signature=abcdef123456&X-Amz-Expires=60: boom\n", []string{"abcdef123456"}},
		{"sig param", "get https://x.example/f?sig=SECRETSIG123 failed\n", []string{"SECRETSIG123"}},
		{"signature word", "bad signature=SIGVAL999 in request\n", []string{"SIGVAL999"}},
		{"aws4 header", "Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260101/x, SignedHeaders=host, Signature=deadbeefcafe\n", []string{"AKIAIOSFODNN7EXAMPLE", "deadbeefcafe"}},
		{"second cookie", "Cookie: session=abc123; other=zzz999\n", []string{"abc123", "zzz999"}},
		{"bare aws key id", "AccessDenied for AKIAIOSFODNN7EXAMPLE on bucket\n", []string{"AKIAIOSFODNN7EXAMPLE"}},
		{"bare jwt", "bad token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abcDEF123_-x here\n", []string{"eyJzdWIiOiIxMjM0In0"}}, // gitleaks:allow — выдуманный образец
		{"plain stays", "portrait read: boom\n", nil},
		{"missing session file stays", "open /data/session.json: no such file or directory\n", nil},
		{"parse session stays", "failed to parse session: unexpected EOF\n", nil},
		{"key colon prose stays", "monium: key: missing\n", nil},
		{"the word bearer stays", "the bearer of bad news\n", nil},
		{"path and errno stay", "write entries: open /data/x: permission denied\n", nil},
		{"serve warning stays", "serve: push off: bad contact\n", nil},
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
			if len(tc.secrets) == 0 {
				if recs[0] != strings.TrimSpace(tc.line) {
					t.Errorf("line changed: %q", recs[0])
				}
				return
			}
			for _, secret := range tc.secrets {
				if strings.Contains(recs[0], secret) {
					t.Errorf("secret %q left in %q", secret, recs[0])
				}
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

// После отказа Monium (503) отправка не останавливается: следующая строка доходит,
// а недоставленная пачка попадает в Dropped (ревью #696: прежний тест этого не проверял).
func TestWriterKeepsSendingAfterMoniumError(t *testing.T) {
	var got received
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := 200
		if calls.Add(1) == 1 {
			status = 503
		}
		got.handler(status)(w, r)
	}))
	defer srv.Close()
	w, err := monium.New(config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	_, _ = w.Write([]byte("lost\n"))
	for deadline := time.Now().Add(3 * time.Second); calls.Load() < 1 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = w.Write([]byte("kept\n"))
	cancel()
	<-done
	recs := records(t, &got)
	if len(recs) != 2 || recs[1] != "kept" {
		t.Errorf("records %q: the line after the 503 must arrive", recs)
	}
	if w.Dropped() != 1 {
		t.Errorf("Dropped = %d, want 1 (the batch answered with 503)", w.Dropped())
	}
}

// Хвост больше одной пачки уходит несколькими запросами и в том же порядке.
func TestWriterSplitsLargeBacklog(t *testing.T) {
	var got received
	srv := httptest.NewServer(got.handler(200))
	defer srv.Close()
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = "line-" + strconv.Itoa(i) + "\n"
	}
	run(t, config(srv.URL), lines...)
	recs := records(t, &got)
	if len(recs) != 250 {
		t.Fatalf("records = %d, want 250", len(recs))
	}
	for i, r := range recs {
		if r != "line-"+strconv.Itoa(i) {
			t.Fatalf("record %d = %q: order broken", i, r)
		}
	}
	if got.count() < 3 {
		t.Errorf("requests = %d: 250 records must not fit into fewer than 3 batches of 100", got.count())
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

// Остановка не должна зависеть от Monium: у контейнера на выход считанные секунды (ревью #696).
func TestWriterStopIsBoundedWhenMoniumHangs(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	cfg := config(srv.URL)
	cfg.FlushTimeout = 300 * time.Millisecond
	w, err := monium.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 250 { // три пачки по 100: без общего срока это 3 запроса по sendTimeout
		_, _ = w.Write([]byte("boom\n"))
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // первая пачка уже ушла и зависла на сервере
	start := time.Now()
	cancel()
	select {
	case <-done:
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("Run took %v to stop", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop within 3 s: the flush has no overall deadline")
	}
	// Сервер завис, ничего не доставлено: каждая из 250 строк либо в пачке, которая не дошла,
	// либо осталась в очереди. Счет должен сойтись ровно (ревью #696, круг 2: мутация выживала).
	if got := w.Dropped(); got != 250 {
		t.Errorf("Dropped = %d, want 250 (nothing was delivered)", got)
	}
}

// Строка после остановки Run не должна пропадать молча: очередь никто не заберет.
func TestWriterCountsLinesWrittenAfterStop(t *testing.T) {
	var got received
	srv := httptest.NewServer(got.handler(200))
	defer srv.Close()
	w := run(t, config(srv.URL), "first\n")
	before := w.Dropped()
	for range 5 {
		if n, err := w.Write([]byte("late\n")); n != 5 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if d := w.Dropped() - before; d != 5 {
		t.Errorf("5 late lines counted as %d dropped", d)
	}
}

// Мегабайтная строка не должна тормозить Write вычисткой (0,2 с на МБ в ревью #696).
func TestWriterCapsLineLength(t *testing.T) {
	var got received
	srv := httptest.NewServer(got.handler(200))
	defer srv.Close()
	run(t, config(srv.URL), "boom "+strings.Repeat("a", 20000)+"\n")
	recs := records(t, &got)
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	if len(recs[0]) > 8300 {
		t.Errorf("record is %d bytes: a long line must be cut", len(recs[0]))
	}
	if !strings.HasPrefix(recs[0], "boom aaaa") {
		t.Errorf("the start of the line must stay: %.20q", recs[0])
	}
}
