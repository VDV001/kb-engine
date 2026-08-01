package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/drift"
)

func mustStatus(t *testing.T, code int) domain.LinkStatus {
	t.Helper()
	s, err := domain.ClassifyLinkStatus(code)
	if err != nil {
		t.Fatalf("ClassifyLinkStatus(%d): %v", code, err)
	}
	return s
}

// The order of the report is a contract, not formatting: a reader who stops
// after the first lines must leave knowing the limits of the scan. This test
// exists so a later tidy-up cannot quietly move the findings above them.
func TestPrintDriftReport_startsWithWhatWasNotEstablished(t *testing.T) {
	now := time.Now()
	rep := drift.Report{
		TotalEntries: 1340,
		WithoutURL:   27,
		Unreachable:  []drift.Unreachable{{EntryID: 9, URL: "https://x/"}},
		Results: []drift.Result{
			{EntryID: 1, URL: "https://a/", Code: 200, Status: mustStatus(t, 200), CheckedAt: now},
			{EntryID: 2, URL: "https://b/", Code: 404, Status: mustStatus(t, 404), CheckedAt: now, Title: "Gone"},
			{EntryID: 3, URL: "https://c/", Code: 403, Status: mustStatus(t, 403), CheckedAt: now},
		},
	}

	var out bytes.Buffer
	printDriftReport(&out, rep)
	s := out.String()

	notChecked := strings.Index(s, "НЕ проверено")
	dead := strings.Index(s, "мёртвые ссылки")
	if notChecked < 0 {
		t.Fatalf("report does not state what was not checked:\n%s", s)
	}
	if dead >= 0 && notChecked > dead {
		t.Errorf("findings printed before the limits of the scan:\n%s", s)
	}
	for _, want := range []string{"27", "1340", "нужен браузер", "id=2"} {
		if !strings.Contains(s, want) {
			t.Errorf("report does not mention %q:\n%s", want, s)
		}
	}
}

func TestPrintDriftReport_saysWhenNothingIsDead(t *testing.T) {
	var out bytes.Buffer
	printDriftReport(&out, drift.Report{TotalEntries: 1, Results: []drift.Result{
		{EntryID: 1, Code: 200, Status: mustStatus(t, 200)},
	}})
	if !strings.Contains(out.String(), "мёртвых ссылок не найдено") {
		t.Errorf("a clean scan does not say so:\n%s", out.String())
	}
}

// runDrift end-to-end against a local server: a 404 must reach the catalog as a
// code, and the entry's own address must survive when --update-urls is absent.
func TestRunDrift_applyRecordsTheCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := fmt.Sprintf(`{"entries":[{"id":1,"title":"T","url":%q,"category":"golang","status":"keep","lifecycle":"active"}]}`, srv.URL)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"drift", "--catalog", path, "--delay", "0", "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := string(parsed.Entries[0]["drift_http_code"]); got != "404" {
		t.Errorf("drift_http_code = %s, want 404", got)
	}
	if _, ok := parsed.Entries[0]["drift_check_date"]; !ok {
		t.Error("the check was not dated")
	}
}

// --update-urls rewrites addresses, so it must not work in a mode that writes
// nothing — a flag that silently does nothing is worse than one that refuses.
func TestRunDrift_updateURLsRequiresApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"drift", "--catalog", path, "--update-urls"}, &out, &errb); code == 0 {
		t.Fatal("--update-urls was accepted without --apply")
	}
	if !strings.Contains(errb.String(), "--apply") {
		t.Errorf("stderr %q does not say what is missing", errb.String())
	}
}

func TestRunDrift_requiresCatalog(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"drift"}, &out, &errb); code == 0 {
		t.Fatal("drift ran without --catalog")
	}
}
