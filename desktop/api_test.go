package main

import (
	"context"
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

	h, x, err := newAPI(config{Roots: []string{root}, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.files.Build(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, want string }{
		{"/api/files/search?q=" + url.QueryEscape("договор"), "plan.md"},
		{"/api/search?q=" + url.QueryEscape("договор"), "Как читать договор"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Host = "wails"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
}

func TestNewAPI_WithoutCatalogStillSearchesFiles(t *testing.T) {
	h, x, err := newAPI(config{Roots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.files.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/files/search?q=x", nil)
	req.Host = "wails"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("files: %d", rec.Code)
	}
}

func TestNormalizeRoots(t *testing.T) {
	// «~» в конфиге и удаленная папка роняли все окно через log.Fatal (ревью
	// #371). Тильда раскрывается, несуществующее пропускается и называется.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots, skipped := normalizeRoots([]string{"~/notes", filepath.Join(home, "gone"), "relative"}, home)
	if len(roots) != 1 || roots[0] != filepath.Join(home, "notes") {
		t.Errorf("roots = %q", roots)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %q, want 2", skipped)
	}
}

func TestNewAPI_MissingRootDoesNotFail(t *testing.T) {
	if _, _, err := newAPI(config{Roots: []string{filepath.Join(t.TempDir(), "gone")}}); err != nil {
		t.Fatalf("newAPI с удаленной папкой: %v", err)
	}
}
