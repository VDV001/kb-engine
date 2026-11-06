package weblink_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/weblink"
)

// Ссылка на документ Google читается его экспортом в текст: страница
// редактора — это скрипты, а не документ.
func TestExportURL_Google(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://docs.google.com/document/d/1AbC_d-9/edit?usp=sharing", "https://docs.google.com/document/d/1AbC_d-9/export?format=txt"},
		{"https://docs.google.com/spreadsheets/d/1xY/edit#gid=42", "https://docs.google.com/spreadsheets/d/1xY/export?format=csv&gid=42"},
		{"https://docs.google.com/spreadsheets/d/1xY/edit", "https://docs.google.com/spreadsheets/d/1xY/export?format=csv"},
		{"https://docs.google.com/presentation/d/1pR/edit#slide=id.p", "https://docs.google.com/presentation/d/1pR/export/txt"},
		{"https://drive.google.com/file/d/1fL/view?usp=drive_link", "https://drive.google.com/uc?export=download&id=1fL"},
		{"https://example.com/a/b", "https://example.com/a/b"},
	}
	for _, tc := range tests {
		if got := weblink.ExportURL(tc.in); got != tc.want {
			t.Errorf("ExportURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Страница — это ее текст: без скриптов, стилей и меню разметки, с
// заголовком из <title>; абзацы и строки таблиц остаются строками.
func TestFetch_HTMLText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title> Тендер 353 </title><style>p{color:red}</style>
			<script>var secret = "x";</script></head><body><nav>меню</nav>
			<svg/><h1>Поставка</h1><p>Срок —&nbsp;30&nbsp;дней.</p><table><tr><td>Цена</td><td>100</td></tr></table>
			<noscript>включите js</noscript></body></html>`)
	}))
	defer srv.Close()
	p, err := weblink.Client{HTTP: srv.Client()}.Fetch(context.Background(), srv.URL+"/t")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Тендер 353" {
		t.Errorf("title = %q", p.Title)
	}
	for _, want := range []string{"Поставка", "Срок — 30 дней.", "Цена 100"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("нет %q в тексте:\n%s", want, p.Text)
		}
	}
	for _, bad := range []string{"secret", "color:red", "включите js", "меню"} {
		if strings.Contains(p.Text, bad) {
			t.Errorf("%q попало в текст:\n%s", bad, p.Text)
		}
	}
}

// Простой текст и CSV — как есть; PDF и docx — байтами с видом файла, их
// читает окно своим распознаванием.
func TestFetch_TextAndFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.csv":
			w.Header().Set("Content-Type", "text/csv")
			_, _ = io.WriteString(w, "a,b\n1,2\n")
		case "/a.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, "%PDF-1.7")
		}
	}))
	defer srv.Close()
	c := weblink.Client{HTTP: srv.Client()}
	p, err := c.Fetch(context.Background(), srv.URL+"/a.csv")
	if err != nil || p.Text != "a,b\n1,2\n" || p.Title != "a.csv" {
		t.Fatalf("csv: %+v, %v", p, err)
	}
	p, err = c.Fetch(context.Background(), srv.URL+"/a.pdf")
	if err != nil || p.Text != "" || p.Ext != "pdf" || string(p.Raw) != "%PDF-1.7" {
		t.Fatalf("pdf: %+v, %v", p, err)
	}
}

// Закрытый документ: Google уводит на вход, сервер отвечает 401/403 — это
// отказ «документ закрыт», а не текст страницы входа.
func TestFetch_Closed(t *testing.T) {
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<title>Вход</title>")
	}))
	defer login.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forbidden" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, login.URL+"/ServiceLogin", http.StatusFound)
	}))
	defer srv.Close()
	c := weblink.Client{HTTP: srv.Client(), LoginHosts: []string{strings.TrimPrefix(login.URL, "http://")}}
	for _, path := range []string{"/doc", "/forbidden"} {
		if _, err := c.Fetch(context.Background(), srv.URL+path); !errors.Is(err, weblink.ErrClosed) {
			t.Errorf("%s: err = %v, want ErrClosed", path, err)
		}
	}
}

// Только http и https; ответ больше потолка — отказ, а не обрезанный текст.
func TestFetch_Refusals(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/a", "javascript:alert(1)", "не ссылка"} {
		if _, err := (weblink.Client{}).Fetch(context.Background(), u); !errors.Is(err, weblink.ErrBadURL) {
			t.Errorf("%q: err = %v, want ErrBadURL", u, err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	}))
	defer srv.Close()
	if _, err := (weblink.Client{HTTP: srv.Client(), MaxBytes: 1024}).Fetch(context.Background(), srv.URL); !errors.Is(err, weblink.ErrTooBig) {
		t.Fatalf("err = %v, want ErrTooBig", err)
	}
	srv404 := httptest.NewServer(http.NotFoundHandler())
	defer srv404.Close()
	if _, err := (weblink.Client{HTTP: srv404.Client()}).Fetch(context.Background(), srv404.URL); !errors.Is(err, weblink.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Ссылки из текста вопроса: без хвостовой пунктуации и кавычек, без повторов.
func TestLinks(t *testing.T) {
	got := weblink.Links(`Глянь «https://docs.google.com/document/d/1A/edit», и https://example.com/x?a=1. Еще раз https://example.com/x?a=1 (https://ya.ru/)`)
	want := []string{"https://docs.google.com/document/d/1A/edit", "https://example.com/x?a=1", "https://ya.ru/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Links = %q, want %q", got, want)
	}
}
