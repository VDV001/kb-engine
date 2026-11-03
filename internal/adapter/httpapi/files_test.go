package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	rec := get(t, newTestServerWith(httpapi.WithFiles(ff)), "/api/files/search?q=%D0%B4%D0%BE%D0%B3%D0%BE%D0%B2%D0%BE%D1%80&limit=5")
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
		rec := get(t, newTestServerWith(httpapi.WithFiles(ff)), "/api/files/search"+tc.query)
		if rec.Code != http.StatusOK || ff.gotLimit != tc.wantLimit {
			t.Errorf("%s: status %d limit %d, want 200/%d", tc.query, rec.Code, ff.gotLimit, tc.wantLimit)
		}
	}
	rec := get(t, newTestServerWith(httpapi.WithFiles(&fakeFiles{})), "/api/files/search")
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
		rec := get(t, srv, "/api/files/read?path="+tc.path)
		if rec.Code != tc.want {
			t.Errorf("read %q: status %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
	rec := get(t, srv, "/api/files/read?path=/w/plan.md")
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
