package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAPI_ServesFilesAndCatalog(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("Подписали договора"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"entries":[{"id":1,"title":"Как читать договор","url":"https://h/a","category":"golang","status":"keep","lifecycle":"active"}]}`
	if err := os.WriteFile(catalog, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	h, svc, err := newAPI(config{Roots: []string{root}, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Build(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, want string }{
		{"/api/files/search?q=" + url.QueryEscape("договор"), "plan.md"},
		{"/api/search?q=" + url.QueryEscape("договор"), "Как читать договор"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
}

func TestNewAPI_WithoutCatalogStillSearchesFiles(t *testing.T) {
	h, svc, err := newAPI(config{Roots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Build(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/files/search?q=x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("files: %d", rec.Code)
	}
}
