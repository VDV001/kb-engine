package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/google"
	"github.com/daniil/kb-engine/internal/adapter/weblink"
)

// fakeGoogle answers the token endpoint and Drive like Google.
func fakeGoogle(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.PostForm.Get("client_secret") != "sec-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3599}`)
		case r.Header.Get("Authorization") != "Bearer at-1":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/drive/about":
			_, _ = io.WriteString(w, `{"user":{"emailAddress":"person@example.com"}}`)
		case r.URL.Path == "/drive/files/doc1":
			_, _ = io.WriteString(w, `{"id":"doc1","name":"ТЗ закрытое","mimeType":"application/vnd.google-apps.document"}`)
		case r.URL.Path == "/drive/files/doc1/export":
			_, _ = io.WriteString(w, "Срок 15 декабря")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestGoogle(t *testing.T) (*googleDesk, *fakeTokens, *folderSettings) {
	t.Helper()
	s, tok, _ := newServerSettings(t)
	srv := fakeGoogle(t)
	g := newGoogleDesk(s)
	g.client = func(id, secret string) google.Client {
		return google.Client{ClientID: id, ClientSecret: secret, AuthBase: "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: srv.URL + "/token", APIURL: srv.URL + "/drive", HTTP: srv.Client()}
	}
	return g, tok, s
}

// Вход: окно открывает страницу согласия, ловит ответ на 127.0.0.1, меняет
// код на токены и сохраняет подключение — секреты в Связке, в config.json
// только вид, подпись и чей вход.
func TestGoogle_ConnectAndRead(t *testing.T) {
	g, tok, s := newTestGoogle(t)
	g.open = func(consent string) {
		u, _ := url.Parse(consent)
		q := u.Query()
		cb := q.Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {q.Get("state")}}.Encode()
		go func() {
			res, err := http.Get(cb)
			if err == nil {
				_ = res.Body.Close()
			}
		}()
	}
	row, err := g.Connect("cid.apps.googleusercontent.com", "sec-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "google" || row.Name != "Google · person@example.com" || !row.OK {
		t.Fatalf("row = %+v", row)
	}
	cfg := s.snapshot().cfg
	if len(cfg.Connections) != 1 || cfg.Connections[0].Account != "person@example.com" || !cfg.Connections[0].Enabled {
		t.Fatalf("config = %+v", cfg.Connections)
	}
	id := cfg.Connections[0].ID
	if tok.m["conn:"+id+":refresh"] != "rt-1" || !strings.Contains(tok.m["conn:"+id+":client"], "sec-1") {
		t.Fatalf("Связка: %v", tok.m)
	}
	// Закрытый документ по ссылке читается через подключение.
	d, err := g.Read(context.Background(), "https://docs.google.com/document/d/doc1/edit")
	if err != nil || d.Text != "Срок 15 декабря" || d.Name != "ТЗ закрытое" {
		t.Fatalf("read: %+v, %v", d, err)
	}
	// В «Подключениях» — строка Google с тем, кто пользуется.
	rows := s.Connections()
	if last := rows[len(rows)-1]; last.Kind != "google" || !strings.Contains(strings.Join(last.UsedBy, ","), "ссылки") {
		t.Fatalf("rows = %+v", rows)
	}
	// Выключено — не читается; удалено — секреты стерты.
	if err := g.SetEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Read(context.Background(), "https://docs.google.com/document/d/doc1/edit"); !errors.Is(err, errNoGoogle) {
		t.Fatalf("выключенное читает: %v", err)
	}
	if err := g.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := tok.m["conn:"+id+":refresh"]; ok || len(s.snapshot().cfg.Connections) != 0 {
		t.Fatalf("после удаления: %v, %+v", tok.m, s.snapshot().cfg.Connections)
	}
}

// Ответ с чужим state — подмена: подключение не создается. Неверный
// клиент — понятный отказ, ни строки в config.json.
func TestGoogle_ConnectRefusals(t *testing.T) {
	g, _, s := newTestGoogle(t)
	g.timeout = 2 * time.Second
	g.open = func(consent string) {
		u, _ := url.Parse(consent)
		cb := u.Query().Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {"чужой"}}.Encode()
		go func() {
			if res, err := http.Get(cb); err == nil {
				_ = res.Body.Close()
			}
		}()
	}
	if _, err := g.Connect("cid.apps.googleusercontent.com", "sec-1"); err == nil {
		t.Fatal("чужой state принят")
	}
	if _, err := g.Connect("не-клиент", "sec-1"); err == nil || !strings.Contains(err.Error(), "Client ID") {
		t.Fatalf("неверный клиент: %v", err)
	}
	g.open = func(consent string) {
		u, _ := url.Parse(consent)
		cb := u.Query().Get("redirect_uri") + "?" + url.Values{"error": {"access_denied"}, "state": {u.Query().Get("state")}}.Encode()
		go func() {
			if res, err := http.Get(cb); err == nil {
				_ = res.Body.Close()
			}
		}()
	}
	if _, err := g.Connect("cid.apps.googleusercontent.com", "sec-1"); err == nil || !strings.Contains(err.Error(), "отказ") {
		t.Fatalf("отказ на странице согласия: %v", err)
	}
	if n := len(s.snapshot().cfg.Connections); n != 0 {
		t.Fatalf("подключений %d после отказов", n)
	}
	_ = desktopconfig.ConnGoogle
}

// Ссылка Google в чате читается через подключение; без подключения — как
// открытая страница (экспорт), чтобы открытые документы работали и так.
func TestReadLink_GoogleThroughConnection(t *testing.T) {
	g, _, _ := newTestGoogle(t)
	g.open = func(consent string) {
		u, _ := url.Parse(consent)
		cb := u.Query().Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {u.Query().Get("state")}}.Encode()
		go func() {
			if res, err := http.Get(cb); err == nil {
				_ = res.Body.Close()
			}
		}()
	}
	link := "https://docs.google.com/document/d/doc1/edit"
	public := weblink.Client{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("открытый текст")), Request: &http.Request{URL: &url.URL{Host: "docs.google.com", Path: "/x"}}}, nil
	})}}
	d, err := readLink(context.Background(), extraction{}, public, g, "url:"+link, link)
	if err != nil || d.Text != "открытый текст" {
		t.Fatalf("без подключения: %+v, %v", d, err)
	}
	if _, err := g.Connect("cid.apps.googleusercontent.com", "sec-1"); err != nil {
		t.Fatal(err)
	}
	d, err = readLink(context.Background(), extraction{}, public, g, "url:"+link, link)
	if err != nil || d.Text != "Срок 15 декабря" || d.Title != "ТЗ закрытое" || d.Source != "Google · person@example.com" {
		t.Fatalf("через подключение: %+v, %v", d, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Ревью #466: Drive не отдал документ (нет доступа аккаунту, API не включен) —
// ссылка читается открытым экспортом, как до подключения; чужой state не
// обрывает вход; пустой refresh-токен — отказ, а не «подключено».
func TestGoogle_ReviewFixes(t *testing.T) {
	g, _, _ := newTestGoogle(t)
	answer := func(state string) func(string) {
		return func(consent string) {
			u, _ := url.Parse(consent)
			cb := u.Query().Get("redirect_uri")
			go func() {
				if state != "" {
					if res, err := http.Get(cb + "?" + url.Values{"code": {"x"}, "state": {state}}.Encode()); err == nil {
						_ = res.Body.Close()
					}
				}
				if res, err := http.Get(cb + "?" + url.Values{"code": {"c1"}, "state": {u.Query().Get("state")}}.Encode()); err == nil {
					_ = res.Body.Close()
				}
			}()
		}
	}
	g.open = answer("чужой")
	if _, err := g.Connect("cid.apps.googleusercontent.com", "sec-1"); err != nil {
		t.Fatalf("чужой state оборвал вход: %v", err)
	}
	public := weblink.Client{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("открытый текст")), Request: &http.Request{URL: &url.URL{Host: "docs.google.com", Path: "/x"}}}, nil
	})}}
	link := "https://docs.google.com/document/d/nope/edit" // Drive отвечает 404
	d, err := readLink(context.Background(), extraction{}, public, g, "url:"+link, link)
	if err != nil || d.Text != "открытый текст" {
		t.Fatalf("откат на открытый экспорт: %+v, %v", d, err)
	}
}
