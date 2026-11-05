package blobs3_test

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobtest"
)

const (
	bucket = "kb-test"
	token  = "t1.fake-iam-token-for-tests"
)

// fakeS3 keeps objects in memory and answers the way Object Storage documents
// for PutObject: If-None-Match: * and If-Match: <etag>, 412 on a failed
// condition. PUT без условия — 400: адаптер не должен уметь писать вслепую.
//
// Это модель, которую написал тот же автор, что и адаптер, — поэтому есть
// TestLiveYandex против настоящего бакета.
type fakeS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	hits atomic.Int64
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{objs: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func etagOf(b []byte) string {
	sum := md5.Sum(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, "AccessDenied", http.StatusForbidden)
		return
	}
	key, ok := strings.CutPrefix(r.URL.Path, "/"+bucket+"/")
	if !ok {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, exists := f.objs[key]
	switch r.Method {
	case http.MethodGet:
		if !exists {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etagOf(cur) {
			w.Header().Set("ETag", inm)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etagOf(cur))
		_, _ = w.Write(cur)
	case http.MethodPut:
		ifMatch, ifNone := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
		switch {
		case (ifMatch == "") == (ifNone == ""):
			http.Error(w, "exactly one of If-Match / If-None-Match", http.StatusBadRequest)
			return
		case ifNone != "" && ifNone != "*":
			http.Error(w, "If-None-Match must be *", http.StatusBadRequest)
			return
		case ifNone == "*" && exists:
			http.Error(w, "PreconditionFailed", http.StatusPreconditionFailed)
			return
		case ifMatch != "" && !exists:
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		case ifMatch != "" && ifMatch != etagOf(cur):
			http.Error(w, "PreconditionFailed", http.StatusPreconditionFailed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.objs[key] = body
		w.Header().Set("ETag", etagOf(body))
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func newStore(t *testing.T, endpoint string, tok blobs3.TokenSource) *blobs3.Store {
	t.Helper()
	s, err := blobs3.New(blobs3.Config{Endpoint: endpoint, Bucket: bucket, Token: tok})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestContract(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		_, srv := newFakeS3(t)
		return newStore(t, srv.URL, blobs3.StaticToken(token))
	})
}

func TestRequestCarriesBearerAndEscapedKey(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.EscapedPath(), r.Header.Get("Authorization")
		http.Error(w, "NoSuchKey", http.StatusNotFound)
	}))
	defer srv.Close()
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	_, _ = s.Get(context.Background(), "users/u1/kb/notes/тема заметки.md")
	want := "/" + bucket + "/users/u1/kb/notes/%D1%82%D0%B5%D0%BC%D0%B0%20%D0%B7%D0%B0%D0%BC%D0%B5%D1%82%D0%BA%D0%B8.md"
	if gotPath != want {
		t.Errorf("path = %s\nwant   %s", gotPath, want)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		put    bool
		status int
		want   error // nil: an error that is neither ErrNotFound nor ErrConflict
	}{
		{"get 404", false, http.StatusNotFound, blob.ErrNotFound},
		{"get 403", false, http.StatusForbidden, nil},
		{"get 500", false, http.StatusInternalServerError, blob.ErrTransient},
		{"get 503", false, http.StatusServiceUnavailable, blob.ErrTransient},
		{"get-if-changed 304", false, http.StatusNotModified, blob.ErrNotModified},
		{"put 412", true, http.StatusPreconditionFailed, blob.ErrConflict},
		{"put 409 concurrent write", true, http.StatusConflict, blob.ErrConflict},
		{"put 404 key gone under If-Match", true, http.StatusNotFound, blob.ErrConflict},
		{"put 403", true, http.StatusForbidden, nil},
		// Yandex на одновременной условной записи в один ключ изредка отвечает
		// 500 InternalError (замер 05.11: раз на ~7 прогонов из четырех писателей).
		{"put 500", true, http.StatusInternalServerError, blob.ErrTransient},
		{"put 503", true, http.StatusServiceUnavailable, blob.ErrTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "<Error>secret body "+token+"</Error>", c.status)
			}))
			defer srv.Close()
			s := newStore(t, srv.URL, blobs3.StaticToken(token))
			var err error
			switch {
			case c.put:
				_, err = s.Put(context.Background(), "k", []byte("v"), `"e"`)
			case c.status == http.StatusNotModified:
				_, err = s.GetIfChanged(context.Background(), "k", `"e"`)
			default:
				_, err = s.Get(context.Background(), "k")
			}
			if err == nil {
				t.Fatal("no error")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == nil && (errors.Is(err, blob.ErrNotFound) || errors.Is(err, blob.ErrConflict) || errors.Is(err, blob.ErrTransient)) {
				t.Fatalf("err = %v: a %d must not look like a missing key or a conflict", err, c.status)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(c.status)) {
				t.Errorf("err %q does not name the status %d", err, c.status)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("err leaks the token: %q", err)
			}
		})
	}
}

func TestGetOverSizeLimitIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"e"`)
		_, _ = w.Write(make([]byte, blob.MaxObjectSize+1))
	}))
	defer srv.Close()
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	if _, err := s.Get(context.Background(), "k"); !errors.Is(err, blob.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestTokenErrorStopsTheRequest(t *testing.T) {
	f, srv := newFakeS3(t)
	broken := tokenFunc(func(context.Context) (string, error) { return "", errors.New("metadata down") })
	s := newStore(t, srv.URL, broken)
	if _, err := s.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get succeeded without a token")
	}
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the bucket without a token", n)
	}
}

type tokenFunc func(context.Context) (string, error)

func (f tokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

func TestNewValidatesConfig(t *testing.T) {
	tok := blobs3.StaticToken(token)
	cases := []struct {
		name string
		cfg  blobs3.Config
		ok   bool
	}{
		{"https endpoint", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: bucket, Token: tok}, true},
		{"plain http to loopback (tests, emulator)", blobs3.Config{Endpoint: "http://127.0.0.1:9000", Bucket: bucket, Token: tok}, true},
		{"plain http to the network", blobs3.Config{Endpoint: "http://storage.yandexcloud.net", Bucket: bucket, Token: tok}, false},
		{"endpoint with a path", blobs3.Config{Endpoint: "https://storage.yandexcloud.net/x", Bucket: bucket, Token: tok}, false},
		{"no bucket", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Token: tok}, false},
		{"bucket with a slash", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: "a/b", Token: tok}, false},
		{"no token source", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: bucket}, false},
		{"bad prefix", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: bucket, Token: tok, Prefix: "../x/"}, false},
		{"prefix without trailing slash", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: bucket, Token: tok, Prefix: "test"}, false},
		{"good prefix", blobs3.Config{Endpoint: "https://storage.yandexcloud.net", Bucket: bucket, Token: tok, Prefix: "test/run1/"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := blobs3.New(c.cfg)
			if (err == nil) != c.ok {
				t.Fatalf("New: err = %v, want ok = %v", err, c.ok)
			}
		})
	}
}

func TestPrefixIsPrepended(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		http.Error(w, "NoSuchKey", http.StatusNotFound)
	}))
	defer srv.Close()
	s, err := blobs3.New(blobs3.Config{Endpoint: srv.URL, Bucket: bucket, Token: blobs3.StaticToken(token), Prefix: "test/run1/"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Get(context.Background(), "k")
	if want := "/" + bucket + "/test/run1/k"; gotPath != want {
		t.Fatalf("path = %s, want %s", gotPath, want)
	}
}

func TestMetadataTokenCachesUntilNearExpiry(t *testing.T) {
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "flavor", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/computeMetadata/v1/instance/service-accounts/default/token" {
			http.Error(w, "path", http.StatusNotFound)
			return
		}
		n := fetches.Add(1)
		_, _ = fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":3600,"token_type":"Bearer"}`, n)
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	m := &blobs3.MetadataToken{BaseURL: srv.URL, Now: func() time.Time { return now }}
	ctx := context.Background()

	for range 3 {
		got, err := m.Token(ctx)
		if err != nil || got != "tok1" {
			t.Fatalf("Token = %q, %v; want tok1 from one fetch", got, err)
		}
	}
	now = now.Add(50 * time.Minute) // still more than the margin before expiry
	if got, _ := m.Token(ctx); got != "tok1" {
		t.Fatalf("after 50 min Token = %q, want cached tok1", got)
	}
	now = now.Add(9 * time.Minute) // 59 min: inside the refresh margin
	if got, _ := m.Token(ctx); got != "tok2" {
		t.Fatalf("near expiry Token = %q, want a fresh tok2", got)
	}
	if n := fetches.Load(); n != 2 {
		t.Fatalf("%d fetches, want 2", n)
	}
}

func TestMetadataTokenRefusesBadAnswers(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status 500": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
		"empty token": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"access_token":"","expires_in":3600}`)
		},
		"not json": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html>`)
		},
		"no lifetime": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":0}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			m := &blobs3.MetadataToken{BaseURL: srv.URL}
			if tok, err := m.Token(context.Background()); err == nil {
				t.Fatalf("Token = %q, want an error", tok)
			}
		})
	}
}

// TestLiveYandex runs the contract against a real Object Storage bucket. Нужны
// KB_BLOB_LIVE_BUCKET и KB_BLOB_LIVE_TOKEN_FILE (IAM-токен, `yc iam
// create-token` в файл 0600). Ключи пишутся под test/<случайное>/, их чистит
// правило жизненного цикла бакета.
func TestLiveYandex(t *testing.T) {
	bkt, tokFile := os.Getenv("KB_BLOB_LIVE_BUCKET"), os.Getenv("KB_BLOB_LIVE_TOKEN_FILE")
	if bkt == "" || tokFile == "" {
		t.Skip("KB_BLOB_LIVE_BUCKET / KB_BLOB_LIVE_TOKEN_FILE not set")
	}
	raw, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatal(err)
	}
	tok := blobs3.StaticToken(strings.TrimSpace(string(raw)))
	run := rand.Text()[:12]
	var n atomic.Int64
	blobtest.Run(t, func(t *testing.T) blob.Store {
		s, err := blobs3.New(blobs3.Config{
			Endpoint: blobs3.YandexEndpoint,
			Bucket:   bkt,
			Token:    tok,
			Prefix:   fmt.Sprintf("test/%s/%d/", run, n.Add(1)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// Предел S3 на ключ — 1024 байта вместе с префиксом: отказ до запроса.
func TestKeyWithPrefixOverS3LimitIsRefused(t *testing.T) {
	f, srv := newFakeS3(t)
	s, err := blobs3.New(blobs3.Config{Endpoint: srv.URL, Bucket: bucket, Token: blobs3.StaticToken(token), Prefix: "test/run1/"})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("k", 1020) // valid alone, 1030 bytes with the prefix
	if _, err := s.Put(context.Background(), key, []byte("v"), ""); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Put: err = %v, want ErrInvalidKey", err)
	}
	if _, err := s.Get(context.Background(), key); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Get: err = %v, want ErrInvalidKey", err)
	}
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the bucket", n)
	}
}

func TestBucketNameEdges(t *testing.T) {
	for _, b := range []string{"-ab", "ab-", ".ab", "ab.", "a..b", "AB-c"} {
		if _, err := blobs3.New(blobs3.Config{Endpoint: blobs3.YandexEndpoint, Bucket: b, Token: blobs3.StaticToken(token)}); err == nil {
			t.Errorf("bucket %q accepted", b)
		}
	}
}

// Сбой сервиса метаданных при обновлении не роняет запросы, пока прежний
// токен еще не истек.
func TestMetadataTokenServesCachedWhileValid(t *testing.T) {
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"tok1","expires_in":3600}`)
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	m := &blobs3.MetadataToken{BaseURL: srv.URL, Now: func() time.Time { return now }}
	ctx := context.Background()
	if _, err := m.Token(ctx); err != nil {
		t.Fatal(err)
	}
	down.Store(true)
	now = now.Add(57 * time.Minute) // inside the refresh margin, not expired
	if got, err := m.Token(ctx); err != nil || got != "tok1" {
		t.Fatalf("Token = %q, %v; want the still-valid tok1", got, err)
	}
	now = now.Add(4 * time.Minute) // expired
	if got, err := m.Token(ctx); err == nil {
		t.Fatalf("Token = %q after expiry with the service down, want an error", got)
	}
}
