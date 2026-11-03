package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

type fakeFiles struct {
	gotQuery string
	gotLimit int
}

func (f *fakeFiles) Search(q string, limit int) ([]filesearch.Hit, error) {
	f.gotQuery, f.gotLimit = q, limit
	d, err := domain.NewFileDoc("/w/plan.md", 42, time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC))
	if err != nil {
		return nil, err
	}
	return []filesearch.Hit{{Doc: d, Score: 1.5, Snippet: "Подписали договора"}}, nil
}

func (f *fakeFiles) Read(path string) (string, error) {
	switch path {
	case "/w/plan.md":
		return "весь текст", nil
	case "/w/.env":
		return "", fmt.Errorf("%w: %q", filesearch.ErrNotAllowed, path)
	}
	return "", fmt.Errorf("open %s: no such file", path)
}

func (f *fakeFiles) Stats() filesearch.Stats { return filesearch.Stats{Indexed: 3059, Skipped: 2} }

func TestFilesSearch_API(t *testing.T) {
	ff := &fakeFiles{}
	rec := getLocal(t, newTestServerWith(httpapi.WithFiles(ff)), "/api/files/search?q=%D0%B4%D0%BE%D0%B3%D0%BE%D0%B2%D0%BE%D1%80&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if ff.gotQuery != "договор" || ff.gotLimit != 5 {
		t.Fatalf("запрос дошел как %q/%d", ff.gotQuery, ff.gotLimit)
	}
	var body struct {
		Indexed int `json:"indexed"`
		Skipped int `json:"skipped"`
		Hits    []struct {
			Path, Name, Kind, Snippet, Modified string
			Size                                int64
		} `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Indexed != 3059 || body.Skipped != 2 || len(body.Hits) != 1 {
		t.Fatalf("body = %+v", body)
	}
	h := body.Hits[0]
	if h.Path != "/w/plan.md" || h.Name != "plan.md" || h.Kind != "doc" || h.Size != 42 ||
		h.Snippet != "Подписали договора" || h.Modified != "2026-11-03T09:00:00Z" {
		t.Fatalf("hit = %+v", h)
	}
}

func TestFilesSearch_DefaultsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		query     string
		wantLimit int
	}{
		{"?q=x", 20},
		{"?q=x&limit=abc", 20},
		{"?q=x&limit=0", 20},
		{"?q=x&limit=100000", 200},
	} {
		ff := &fakeFiles{}
		rec := getLocal(t, newTestServerWith(httpapi.WithFiles(ff)), "/api/files/search"+tc.query)
		if rec.Code != http.StatusOK || ff.gotLimit != tc.wantLimit {
			t.Errorf("%s: status %d limit %d, want 200/%d", tc.query, rec.Code, ff.gotLimit, tc.wantLimit)
		}
	}
	rec := getLocal(t, newTestServerWith(httpapi.WithFiles(&fakeFiles{})), "/api/files/search")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("без q ждем 400, получили %d", rec.Code)
	}
}

func TestFilesRead_API(t *testing.T) {
	srv := newTestServerWith(httpapi.WithFiles(&fakeFiles{}))
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/w/plan.md", http.StatusOK},
		{"/w/.env", http.StatusForbidden},
		{"/w/missing.md", http.StatusNotFound},
		{"", http.StatusBadRequest},
	} {
		rec := getLocal(t, srv, "/api/files/read?path="+tc.path)
		if rec.Code != tc.want {
			t.Errorf("read %q: status %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
	rec := getLocal(t, srv, "/api/files/read?path=/w/plan.md")
	var body struct{ Path, Text string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Text != "весь текст" {
		t.Fatalf("body = %+v, err %v", body, err)
	}
}

func TestFiles_AbsentWithoutOption(t *testing.T) {
	// Без источника файлов маршрута нет: сервер, поднятый только над каталогом,
	// не должен делать вид, что умеет искать по диску.
	rec := get(t, newTestServer(), "/api/files/search?q=x")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

type unbuiltFiles struct{ fakeFiles }

func (unbuiltFiles) Search(string, int) ([]filesearch.Hit, error) { return nil, filesearch.ErrNotBuilt }

func TestFilesSearch_NotBuiltYet(t *testing.T) {
	// Индекс собирается в фоне после старта: пока его нет, ответ 503 «еще
	// собирается», а не 500 — окно по нему показывает экран индексации.
	rec := getLocal(t, newTestServerWith(httpapi.WithFiles(&unbuiltFiles{})), "/api/files/search?q=x")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// getLocal — запрос, каким его шлет браузер на этой машине: Host loopback.
func getLocal(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFiles_RefuseForeignHost(t *testing.T) {
	// DNS rebinding: чужой домен перепривязан на 127.0.0.1, и страница читает
	// личные файлы как same-origin (ревью #371). Host обязан быть локальным.
	srv := newTestServerWith(httpapi.WithFiles(&fakeFiles{}))
	for _, tc := range []struct {
		host string
		want int
	}{
		{"evil.example:8080", http.StatusForbidden},
		{"127.0.0.1.evil.example", http.StatusForbidden},
		{"127.0.0.1:8080", http.StatusOK},
		{"localhost:18092", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"wails", http.StatusOK}, // окно Wails ходит в API по wails://wails
		{"wails.localhost", http.StatusOK},
	} {
		for _, path := range []string{"/api/files/search?q=x", "/api/files/read?path=/w/plan.md"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("Host %q %s: %d, want %d", tc.host, path, rec.Code, tc.want)
			}
		}
	}
}

type failedFiles struct{ fakeFiles }

func (failedFiles) Search(string, int) ([]filesearch.Hit, error) {
	return nil, fmt.Errorf("%w: disk gone", filesearch.ErrBuildFailed)
}

func TestFilesSearch_BuildFailedIsAnError(t *testing.T) {
	rec := getLocal(t, newTestServerWith(httpapi.WithFiles(&failedFiles{})), "/api/files/search?q=x")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "disk gone") {
		t.Fatalf("status %d body %q, want 500 with the cause", rec.Code, rec.Body)
	}
}
