package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func TestRun_audit(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"Статья удалена","url":"https://h/","category":"golang","status":"keep"},
		{"id":2,"habr_id":2,"title":"Fresh take","url":"https://h/","category":"golang","status":"keep"}
	]}`)

	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "id=1") {
		t.Errorf("expected candidate id=1 in output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "id=2") {
		t.Errorf("clean entry id=2 should not be reported:\n%s", out.String())
	}
}

func TestRun_missingCatalog(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", "/no/such/file.json"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for missing catalog")
	}
}

func TestRun_catalogFlagRequired(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"audit"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit when --catalog is missing")
	}
}

func TestRun_unknownCheck(t *testing.T) {
	path := writeCatalog(t, `{"entries":[]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "bogus"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for unknown --check")
	}
}

func TestRun_checkFilter(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"Статья удалена","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "canonical"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	// outdated finding (id=1) must NOT appear when only canonical is selected
	if strings.Contains(out.String(), "id=1") {
		t.Errorf("canonical-only run leaked outdated finding:\n%s", out.String())
	}
}

func TestRun_dedup(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"A","url":"https://dup/x","category":"golang","status":"keep"},
		{"id":2,"habr_id":2,"title":"B","url":"https://dup/x","category":"golang","status":"keep"}
	]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"dedup", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "exact-url") {
		t.Errorf("expected an exact-url duplicate group:\n%s", out.String())
	}
}

func TestServe_handler(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	h, err := buildServeHandler(path, "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("body missing total: %s", rec.Body.String())
	}
}

// Serving finances means reading two sources at once: the ledger holds the
// rows, but the account balances only exist in the workbook's «Счета» sheet.
func TestServe_handler_finances(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	xlsx := workbook(t)
	ledger := filepath.Join(filepath.Dir(xlsx), "transactions.jsonl")
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync --init: %s", errb.String())
	}

	h, err := buildServeHandler(catalog, "", ledger, xlsx, "", "", "", "", "")
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/finances", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Accounts     []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Transactions) == 0 {
		t.Error("no transactions served from the ledger")
	}
	if len(body.Accounts) == 0 {
		t.Error("no account balances served from the workbook")
	}
}

// Without a ledger the dashboard still serves; finances are simply empty.
func TestServe_handler_withoutLedger(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	h, err := buildServeHandler(catalog, "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/finances", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"transactions":[]`) {
		t.Errorf("body = %s, want empty transactions", rec.Body.String())
	}
}

func TestRun_version(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "kbengine ") {
		t.Errorf("expected output to start with %q, got: %s", "kbengine ", out.String())
	}
}

func TestRun_unknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}

func TestRun_noArgs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit with no args")
	}
}
