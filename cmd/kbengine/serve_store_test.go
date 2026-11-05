package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

func TestParseS3Location(t *testing.T) {
	cases := []struct {
		in          string
		bucket, key string
		s3          bool
		bad         bool
	}{
		{in: "/Users/me/kb/_data/catalog.json"},
		{in: "catalog.json"},
		{in: "s3://dvk-kb/users/owner/kb/_data/catalog.json", bucket: "dvk-kb", key: "users/owner/kb/_data/catalog.json", s3: true},
		{in: "s3://dvk-kb/catalog.json", bucket: "dvk-kb", key: "catalog.json", s3: true},
		{in: "s3://dvk-kb", s3: true, bad: true},
		{in: "s3://dvk-kb/", s3: true, bad: true},
		{in: "s3:///catalog.json", s3: true, bad: true},
		{in: "s3://dvk-kb/users/../u2/catalog.json", s3: true, bad: true},
		{in: "s3://dvk-kb/a//b.json", s3: true, bad: true},
		{in: "s3://dvk-kb/c.json?x=1", s3: true, bad: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			bucket, key, s3, err := parseS3Location(c.in)
			if s3 != c.s3 {
				t.Fatalf("s3 = %v, want %v", s3, c.s3)
			}
			if (err != nil) != c.bad {
				t.Fatalf("err = %v, want bad = %v", err, c.bad)
			}
			if !c.bad && (bucket != c.bucket || key != c.key) {
				t.Fatalf("got %q %q, want %q %q", bucket, key, c.bucket, c.key)
			}
		})
	}
}

// Каталог, заметка и словарь синонимов лежат в хранилище под одним корнем
// пользователя — так же, как в knowledge-base на диске. Сервер собирается из
// хранилища и отвечает тем же, что из файлов.
func TestServeFromStore(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(key, data string) {
		t.Helper()
		if _, err := s.Put(ctx, key, []byte(data), ""); err != nil {
			t.Fatal(err)
		}
	}
	put("users/owner/kb/_data/catalog.json", `{"entries":[
		{"id":1,"habr_id":1,"title":"Кеширование","url":"https://h/1","category":"golang","status":"keep"},
		{"id":2,"title":"Разбор","type":"note","category":"golang","status":"keep","file":"notes/2026-11-05_разбор.md"}
	]}`)
	put("users/owner/kb/notes/2026-11-05_разбор.md", "# Разбор\n")
	put("users/owner/kb/notes/personal.md", "не для витрины\n")
	put("users/owner/kb/_data/search-synonyms.json", `{"redis":["кеширование"]}`)

	var stderr bytes.Buffer
	src, err := storeCatalogSource(ctx, s, "users/owner/kb/_data/catalog.json", &stderr)
	if err != nil {
		t.Fatalf("storeCatalogSource: %v", err)
	}
	if src.synonyms == nil {
		t.Fatalf("synonyms not loaded from the store; stderr: %s", stderr.String())
	}
	// Синонимы источника применяются сборкой сами: передавать их вторым
	// путем вызывающему не нужно.
	h, err := buildServeHandlerFrom(src, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatalf("buildServeHandlerFrom: %v", err)
	}
	for _, c := range []struct {
		path string
		code int
		has  string
	}{
		{"/api/stats", http.StatusOK, `"total":2`},
		{"/kb/notes/2026-11-05_разбор.md", http.StatusOK, "# Разбор"},
		{"/kb/notes/personal.md", http.StatusNotFound, ""},
		{"/api/search?q=redis", http.StatusOK, "Кеширование"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.code {
			t.Errorf("%s: status %d, want %d; body %.200s", c.path, rec.Code, c.code, rec.Body.String())
			continue
		}
		if c.has != "" && !strings.Contains(rec.Body.String(), c.has) {
			t.Errorf("%s: body lacks %q: %.300s", c.path, c.has, rec.Body.String())
		}
	}
}

// Без словаря в хранилище сервер работает, но говорит, что перевода терминов
// нет — как и с диска.
func TestServeFromStoreWithoutSynonyms(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "kb/_data/catalog.json", []byte(`{"entries":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	src, err := storeCatalogSource(ctx, s, "kb/_data/catalog.json", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if src.synonyms != nil {
		t.Fatal("synonyms option set without a dictionary")
	}
	if !strings.Contains(stderr.String(), "не переводит термины") {
		t.Fatalf("stderr = %q, want the no-dictionary warning", stderr.String())
	}
}

// Каталога в хранилище нет — отказ при старте, а не 500 на каждый запрос.
func TestServeFromStoreRefusesAMissingCatalog(t *testing.T) {
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if _, err := storeCatalogSource(context.Background(), s, "kb/_data/catalog.json", &stderr); err == nil {
		t.Fatal("storeCatalogSource accepted a missing catalog")
	}
}

// IAM-токен с Мака живет до 12 часов: свежий токен в том же файле подхватывается
// без перезапуска serve.
func TestFileTokenRereadsAChangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iam.token")
	write := func(tok string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	first := strings.Repeat("a", 40)
	write(first, t0)
	src, err := newFileToken(path)
	if err != nil {
		t.Fatalf("newFileToken: %v", err)
	}
	if got, err := src.Token(context.Background()); err != nil || got != first {
		t.Fatalf("Token = %q, %v; want the first token", got, err)
	}
	second := strings.Repeat("b", 40)
	write(second, t0.Add(12*time.Hour))
	if got, err := src.Token(context.Background()); err != nil || got != second {
		t.Fatalf("after rewrite Token = %q, %v; want the second token", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, t0.Add(13*time.Hour), t0.Add(13*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(context.Background()); err == nil {
		t.Fatal("a token file readable by others was accepted")
	}
}
