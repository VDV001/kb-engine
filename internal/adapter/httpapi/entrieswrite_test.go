package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

type fakeWriter struct {
	addErr, editErr error
	added           httpapi.EntryAdd
	editedID        int
	edited          httpapi.EntryPatch
	updated         int
}

func (f *fakeWriter) AddEntry(_ context.Context, a httpapi.EntryAdd) (httpapi.AddedEntry, error) {
	f.added = a
	if f.addErr != nil {
		return httpapi.AddedEntry{}, f.addErr
	}
	return httpapi.AddedEntry{ID: 42, Title: a.Title}, nil
}

func (f *fakeWriter) EditEntry(_ context.Context, id int, p httpapi.EntryPatch) (int, error) {
	f.editedID, f.edited = id, p
	if f.editErr != nil {
		return 0, f.editErr
	}
	return f.updated, nil
}

func writeServer(w httpapi.CatalogWriter) http.Handler {
	var opts []httpapi.Option
	if w != nil {
		opts = append(opts, httpapi.WithCatalogWriter(w))
	}
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil, opts...)
}

func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPostEntry(t *testing.T) {
	fw := &fakeWriter{}
	rec := send(writeServer(fw), http.MethodPost, "/api/entries",
		`{"title":"Кеширование","category":"golang","url":"https://h/1","verdict":"keep","tags":["go","cache"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":42`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	if fw.added.Title != "Кеширование" || fw.added.URL != "https://h/1" || len(fw.added.Tags) != 2 {
		t.Fatalf("writer got %+v", fw.added)
	}
}

func TestPostEntryAlreadyThere(t *testing.T) {
	fw := &fakeWriter{}
	h := writeServer(&alreadyWriter{fakeWriter: fw})
	rec := send(h, http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"already":true`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

type alreadyWriter struct{ *fakeWriter }

func (a *alreadyWriter) AddEntry(context.Context, httpapi.EntryAdd) (httpapi.AddedEntry, error) {
	return httpapi.AddedEntry{ID: 7, Title: "A", Already: true}, nil
}

func TestWriteErrorsMapToStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"bad request from the rules", fmt.Errorf("%w: категория не объявлена", httpapi.ErrBadWrite), http.StatusBadRequest},
		{"conflict after retries", fmt.Errorf("catalog: %w", blob.ErrConflict), http.StatusConflict},
		{"no such entry", fmt.Errorf("%w: 3", httpapi.ErrNoEntry), http.StatusNotFound},
		{"store down", errors.New("blobs3: put: status 503"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := writeServer(&fakeWriter{addErr: c.err, editErr: c.err})
			if rec := send(h, http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/1"}`); rec.Code != c.code {
				t.Errorf("POST: %d, want %d", rec.Code, c.code)
			}
			if rec := send(h, http.MethodPatch, "/api/entries/3", `{"verdict":"keep"}`); rec.Code != c.code {
				t.Errorf("PATCH: %d, want %d", rec.Code, c.code)
			}
		})
	}
	// 500 не пересказывает внутреннюю причину: в ней ключ хранилища.
	h := writeServer(&fakeWriter{addErr: errors.New("blobs3: put users/u1/kb/_data/catalog.json: status 503")})
	if rec := send(h, http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/1"}`); strings.Contains(rec.Body.String(), "users/") {
		t.Errorf("500 body leaks the key: %s", rec.Body.String())
	}
}

func TestBadBodiesAreRefused(t *testing.T) {
	fw := &fakeWriter{}
	h := writeServer(fw)
	for _, body := range []string{
		``,
		`{`,
		`{"title":"A","nope":1}`,
		`[1,2]`,
		`{"title":"A"}{"title":"B"}`,
		`{"title":"` + strings.Repeat("x", 70<<10) + `"}`,
	} {
		if rec := send(h, http.MethodPost, "/api/entries", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %.40q: %d, want 400", body, rec.Code)
		}
	}
	for _, path := range []string{"/api/entries/abc", "/api/entries/0", "/api/entries/-1"} {
		if rec := send(h, http.MethodPatch, path, `{"verdict":"keep"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s: %d, want 400", path, rec.Code)
		}
	}
	if rec := send(h, http.MethodPatch, "/api/entries/3", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH with nothing to change: %d, want 400", rec.Code)
	}
}

func TestPatchEntry(t *testing.T) {
	fw := &fakeWriter{}
	rec := send(writeServer(fw), http.MethodPatch, "/api/entries/3", `{"verdict":"consider","lifecycle":"outdated","add_tags":["x"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if fw.editedID != 3 || fw.edited.Verdict == nil || *fw.edited.Verdict != "consider" || fw.edited.Lifecycle == nil || len(fw.edited.AddTags) != 1 {
		t.Fatalf("writer got id %d %+v", fw.editedID, fw.edited)
	}
	if fw.edited.Title != nil {
		t.Fatal("an absent field reached the writer as set")
	}
}

// Без писателя маршрутов записи нет: сервер с диска остается только для
// чтения, и об этом говорит код, а не тихий успех.
func TestNoWriterNoWriteRoutes(t *testing.T) {
	h := writeServer(nil)
	if rec := send(h, http.MethodPost, "/api/entries", `{"title":"A"}`); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("POST without a writer: %d", rec.Code)
	}
	if rec := send(h, http.MethodPatch, "/api/entries/1", `{"verdict":"keep"}`); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH without a writer: %d", rec.Code)
	}
}

// Запись только JSON-запросом: «простой» запрос с чужой страницы (text/plain,
// form) браузер шлет без предварительной проверки, и без этого отказа любая
// открытая страница писала бы в каталог сервера на 127.0.0.1 без токена.
func TestWriteNeedsJSONContentType(t *testing.T) {
	h := writeServer(&fakeWriter{updated: 1})
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		for _, m := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/1"}`},
			{http.MethodPatch, "/api/entries/3", `{"verdict":"keep"}`},
		} {
			req := httptest.NewRequest(m.method, m.path, strings.NewReader(m.body))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s %s with Content-Type %q: %d, want 415", m.method, m.path, ct, rec.Code)
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(`{"title":"A","category":"golang","url":"https://h/1"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("application/json; charset=utf-8: %d, want 201", rec.Code)
	}
}

func TestCrossSiteWriteIsRefused(t *testing.T) {
	h := writeServer(&fakeWriter{updated: 1})
	cases := []struct {
		name    string
		headers map[string]string
		code    int
	}{
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same origin", map[string]string{"Origin": "http://example.com"}, http.StatusCreated},
		{"no origin (CLI, window)", nil, http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://example.com/api/entries", strings.NewReader(`{"title":"A","category":"golang","url":"https://h/1"}`))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.code {
				t.Errorf("%d, want %d (%s)", rec.Code, c.code, rec.Body.String())
			}
		})
	}
}

func TestOversizeBodyIs413(t *testing.T) {
	rec := send(writeServer(&fakeWriter{}), http.MethodPost, "/api/entries", `{"title":"`+strings.Repeat("x", 70<<10)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("70 KB body: %d, want 413", rec.Code)
	}
}

// PATCH называет настоящее число измененных записей: «готово» без правки —
// та же тишина, от которой починен set (reportSet).
func TestPatchReportsTheRealCount(t *testing.T) {
	rec := send(writeServer(&fakeWriter{updated: 0}), http.MethodPatch, "/api/entries/3", `{"verdict":"keep"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":0`) {
		t.Fatalf("no-op PATCH: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPostSaysTheCategoryWasUnchecked(t *testing.T) {
	rec := send(writeServer(&uncheckedWriter{}), http.MethodPost, "/api/entries", `{"title":"A","category":"golang","url":"https://h/1"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"category_unchecked":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

type uncheckedWriter struct{ fakeWriter }

func (u *uncheckedWriter) AddEntry(context.Context, httpapi.EntryAdd) (httpapi.AddedEntry, error) {
	return httpapi.AddedEntry{ID: 5, Title: "A", CategoryUnchecked: true}, nil
}
