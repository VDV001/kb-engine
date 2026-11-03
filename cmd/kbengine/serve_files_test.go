package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeFiles_searchesConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("Подписали договора"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	opt, built := filesOption([]string{root}, nil, &log)
	select {
	case err := <-built:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("индекс не собрался за 10 с")
	}
	h, err := buildServeHandler(baseWithCatalog(t), "", "", "", "", "", "", "", "", nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/files/search?q=договор", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plan.md") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(log.String(), "1 files") {
		t.Errorf("сборка индекса не названа в логе: %q", log.String())
	}
}
