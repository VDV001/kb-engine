package google_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/google"
)

// Вход — OAuth для настольных приложений: PKCE S256, ответ на 127.0.0.1,
// только чтение Диска; refresh-токен просится явно (offline + consent).
func TestAuthURL(t *testing.T) {
	c := google.Client{ClientID: "cid.apps.googleusercontent.com"}
	u, err := url.Parse(c.AuthURL("http://127.0.0.1:53111/callback", "st4te", "verifier-verifier-verifier-verifier-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	sum := sha256.Sum256([]byte("verifier-verifier-verifier-verifier-verifier"))
	want := map[string]string{
		"client_id": "cid.apps.googleusercontent.com", "redirect_uri": "http://127.0.0.1:53111/callback", "response_type": "code",
		"scope": google.ScopeDriveReadonly, "state": "st4te", "code_challenge_method": "S256",
		"code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "access_type": "offline", "prompt": "consent",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if u.Host != "accounts.google.com" {
		t.Errorf("host = %s", u.Host)
	}
}

// Обмен кода и обновление токена — формой на token endpoint; секрет не
// попадает в текст ошибки.
func TestTokens(t *testing.T) {
	var forms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.PostForm)
		if r.PostForm.Get("grant_type") == "refresh_token" && r.PostForm.Get("refresh_token") == "revoked" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"secret-xyz"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3599}`)
	}))
	defer srv.Close()
	c := google.Client{ClientID: "cid", ClientSecret: "secret-xyz", TokenURL: srv.URL, HTTP: srv.Client()}
	tok, err := c.Exchange(context.Background(), "code-1", "http://127.0.0.1:1/callback", "ver")
	if err != nil || tok.Access != "at-1" || tok.Refresh != "rt-1" {
		t.Fatalf("exchange: %+v, %v", tok, err)
	}
	f := forms[0]
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "code-1" || f.Get("code_verifier") != "ver" || f.Get("client_secret") != "secret-xyz" {
		t.Fatalf("form = %v", f)
	}
	if at, err := c.Refresh(context.Background(), "rt-1"); err != nil || at != "at-1" {
		t.Fatalf("refresh: %q, %v", at, err)
	}
	_, err = c.Refresh(context.Background(), "revoked")
	if !errors.Is(err, google.ErrReconnect) || strings.Contains(err.Error(), "secret-xyz") {
		t.Fatalf("revoked: %v", err)
	}
}

// id файла — из любой формы ссылки Google; чужая ссылка — не файл Google.
func TestFileID(t *testing.T) {
	tests := map[string]string{
		"https://docs.google.com/document/d/1AbC_d-9/edit?usp=sharing": "1AbC_d-9",
		"https://docs.google.com/spreadsheets/d/1xY/edit#gid=0":        "1xY",
		"https://docs.google.com/presentation/d/1pR/edit":              "1pR",
		"https://drive.google.com/file/d/1fL/view?usp=drive_link":      "1fL",
		"https://drive.google.com/open?id=1oP":                         "1oP",
		"https://drive.google.com/drive/folders/1Fo?usp=sharing":       "1Fo",
		"https://drive.google.com/drive/u/0/folders/1Fo2":              "1Fo2",
		"https://example.com/document/d/1AbC":                          "",
		"https://docs.google.com.evil.example/document/d/1AbC/edit":    "",
	}
	for in, want := range tests {
		if got := google.FileID(in); got != want {
			t.Errorf("FileID(%q) = %q, want %q", in, got, want)
		}
	}
}

// Документ Google — экспортом в текст (таблица — CSV), остальное — байтами;
// нет доступа и нет файла — свои отказы.
func TestRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/files/doc1") && r.URL.Query().Get("alt") == "":
			_, _ = io.WriteString(w, `{"id":"doc1","name":"ТЗ","mimeType":"application/vnd.google-apps.document"}`)
		case strings.HasSuffix(r.URL.Path, "/files/doc1/export"):
			if r.URL.Query().Get("mimeType") != "text/plain" {
				t.Errorf("export mime = %q", r.URL.Query().Get("mimeType"))
			}
			_, _ = io.WriteString(w, "Срок 30 дней")
		case strings.HasSuffix(r.URL.Path, "/files/sh1") && r.URL.Query().Get("alt") == "":
			_, _ = io.WriteString(w, `{"id":"sh1","name":"Смета","mimeType":"application/vnd.google-apps.spreadsheet"}`)
		case strings.HasSuffix(r.URL.Path, "/files/sh1/export"):
			_, _ = io.WriteString(w, "a,b\n1,2\n")
		case strings.HasSuffix(r.URL.Path, "/files/pdf1") && r.URL.Query().Get("alt") == "media":
			_, _ = io.WriteString(w, "%PDF-1.7")
		case strings.HasSuffix(r.URL.Path, "/files/pdf1"):
			_, _ = io.WriteString(w, `{"id":"pdf1","name":"скан.pdf","mimeType":"application/pdf"}`)
		case strings.HasSuffix(r.URL.Path, "/files/closed"):
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := google.Client{APIURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	d, err := c.Read(ctx, "at-1", "doc1")
	if err != nil || d.Name != "ТЗ" || d.Text != "Срок 30 дней" {
		t.Fatalf("doc: %+v, %v", d, err)
	}
	if d, err = c.Read(ctx, "at-1", "sh1"); err != nil || d.Text != "a,b\n1,2\n" {
		t.Fatalf("sheet: %+v, %v", d, err)
	}
	if d, err = c.Read(ctx, "at-1", "pdf1"); err != nil || d.Ext != "pdf" || string(d.Raw) != "%PDF-1.7" || d.Text != "" {
		t.Fatalf("pdf: %+v, %v", d, err)
	}
	if _, err = c.Read(ctx, "at-1", "closed"); !errors.Is(err, google.ErrNoAccess) {
		t.Fatalf("closed: %v", err)
	}
	if _, err = c.Read(ctx, "at-1", "nope"); !errors.Is(err, google.ErrNotFound) {
		t.Fatalf("nope: %v", err)
	}
	if _, err = c.Read(ctx, "bad", "doc1"); !errors.Is(err, google.ErrReconnect) {
		t.Fatalf("bad token: %v", err)
	}
}

// Кто вошел — для подписи подключения.
func TestAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/about") || r.URL.Query().Get("fields") != "user(emailAddress,displayName)" {
			t.Errorf("about: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"user":{"emailAddress":"person@example.com","displayName":"Person"}}`)
	}))
	defer srv.Close()
	email, err := google.Client{APIURL: srv.URL, HTTP: srv.Client()}.Account(context.Background(), "at-1")
	if err != nil || email != "person@example.com" {
		t.Fatalf("account: %q, %v", email, err)
	}
}
