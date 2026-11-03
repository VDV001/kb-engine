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
)

func TestServeFiles_searchesConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("Подписали договора"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	opt, built := filesOption(context.Background(), []string{root}, nil, &log)
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
	req := httptest.NewRequest(http.MethodGet, "/api/files/search?q=договор", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plan.md") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(log.String(), "1 files") {
		t.Errorf("сборка индекса не названа в логе: %q", log.String())
	}
}
