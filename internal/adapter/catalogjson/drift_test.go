package catalogjson_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

func driftCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"entries":[
{"id":1,"title":"Alive","url":"https://example.com/a","category":"ai-agents-tools","status":"keep","lifecycle":"active"},
{"id":2,"title":"Gone","url":"https://example.com/b","category":"ai-agents-tools","status":"keep","lifecycle":"active"},
{"id":3,"title":"Was blocked, now fine","url":"https://example.com/c","category":"ai-agents-tools","status":"keep","lifecycle":"active","drift_check_date":"2026-05-16","drift_http_code":403},
{"id":4,"title":"Untouched","url":"https://example.com/d","category":"ai-agents-tools","status":"keep","lifecycle":"active"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func member(t *testing.T, path string, id int, key string) (json.RawMessage, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, e := range doc.Entries {
		var got int
		if err := json.Unmarshal(e["id"], &got); err != nil {
			t.Fatalf("parse id: %v", err)
		}
		if got == id {
			v, ok := e[key]
			return v, ok
		}
	}
	t.Fatalf("no entry %d", id)
	return nil, false
}

// A scan whose result lives only in a terminal is a scan the base does not
// remember: close the window and nobody knows what was checked. The date goes
// on every checked entry; the code only when it is not 200, which is the shape
// the catalog already uses.
func TestApplyDrift(t *testing.T) {
	path := driftCatalog(t)
	day := time.Date(2026, 8, 1, 15, 0, 0, 0, time.UTC)

	n, err := catalogjson.ApplyDrift(path, []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: day, Code: 200},
		{EntryID: 2, CheckedAt: day, Code: 404},
		{EntryID: 3, CheckedAt: day, Code: 200},
	})
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	if n != 3 {
		t.Fatalf("updated %d entries, want 3", n)
	}

	for _, id := range []int{1, 2, 3} {
		got, ok := member(t, path, id, "drift_check_date")
		if !ok || string(got) != `"2026-08-01"` {
			t.Errorf("entry %d drift_check_date = %s (present: %v), want \"2026-08-01\"", id, got, ok)
		}
	}

	if _, ok := member(t, path, 1, "drift_http_code"); ok {
		t.Error("a 200 wrote a code: only an anomaly is worth storing")
	}
	code, ok := member(t, path, 2, "drift_http_code")
	if !ok || string(code) != "404" {
		t.Errorf("entry 2 drift_http_code = %s (present: %v), want 404", code, ok)
	}

	// The one that matters most: an entry that was 403 in May and answers 200
	// now must LOSE its code. Leaving it would keep asserting a problem that
	// the scan just disproved.
	if _, ok := member(t, path, 3, "drift_http_code"); ok {
		t.Error("entry 3 kept its stale 403 after answering 200")
	}

	// An entry not in the results is not touched at all.
	if _, ok := member(t, path, 4, "drift_check_date"); ok {
		t.Error("entry 4 was stamped though it was never checked")
	}
}

func TestApplyDrift_emptyResultsWriteNothing(t *testing.T) {
	path := driftCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	n, err := catalogjson.ApplyDrift(path, nil)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	if n != 0 {
		t.Fatalf("updated %d entries, want 0", n)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("an empty scan rewrote the catalog")
	}
}

// A result naming an entry the catalog does not have is a bug in the caller,
// and it must not leave the file half-written.
func TestApplyDrift_unknownEntryWritesNothing(t *testing.T) {
	path := driftCatalog(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if _, err := catalogjson.ApplyDrift(path, []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: time.Now(), Code: 200},
		{EntryID: 999, CheckedAt: time.Now(), Code: 200},
	}); err == nil {
		t.Fatal("ApplyDrift accepted an unknown entry id")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a refused apply still wrote to the catalog")
	}
}

// Habr moved 179 of the catalog's addresses. Writing the canonical one is a
// separate decision from stamping the check: an address is what the entry IS,
// and overwriting it on every scan would let one bad redirect rewrite the base.
func TestApplyDrift_writesTheNewURLOnlyWhenAsked(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	records := []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: day, Code: 302, NewURL: "https://example.com/moved"},
	}

	t.Run("not asked", func(t *testing.T) {
		path := driftCatalog(t)
		if _, err := catalogjson.ApplyDrift(path, records); err != nil {
			t.Fatalf("ApplyDrift: %v", err)
		}
		url, _ := member(t, path, 1, "url")
		if string(url) != `"https://example.com/a"` {
			t.Fatalf("url = %s, want the original — the new address was not asked for", url)
		}
	})

	t.Run("asked", func(t *testing.T) {
		path := driftCatalog(t)
		if _, err := catalogjson.ApplyDriftWithURLs(path, records); err != nil {
			t.Fatalf("ApplyDriftWithURLs: %v", err)
		}
		url, _ := member(t, path, 1, "url")
		if string(url) != `"https://example.com/moved"` {
			t.Fatalf("url = %s, want the redirect target", url)
		}
		// The check itself is still recorded — updating an address does not
		// replace saying when it was verified.
		if date, ok := member(t, path, 1, "drift_check_date"); !ok || string(date) != `"2026-08-01"` {
			t.Errorf("drift_check_date = %s (present %v)", date, ok)
		}
	})
}

// Повторный прогон в тот же день записывает те же значения — файл не меняется
// ни на байт, а отчёт до 28.08.2026 всё равно говорил «записано N записей».
// Это ровно класс «команда отчиталась успехом, ничего не изменив», против
// которого в движке стоит отдельный гейт: счёт шёл по совпавшим записям,
// а не по изменившимся.
//
// Прецедент: `drift --limit 6 --apply` напечатал «записано в каталог:
// 6 записей», при этом `cmp` показал, что catalog.json не изменился вовсе.
func TestApplyDrift_countsChangedEntriesNotMatchedOnes(t *testing.T) {
	path := driftCatalog(t)
	checked := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	records := []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: checked, Code: 200},
		{EntryID: 2, CheckedAt: checked, Code: 404},
	}

	n, err := catalogjson.ApplyDrift(path, records)
	if err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}
	if n != 2 {
		t.Fatalf("первый прогон записал %d, ожидалось 2", n)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	n, err = catalogjson.ApplyDrift(path, records)
	if err != nil {
		t.Fatalf("ApplyDrift (повтор): %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !bytes.Equal(before, after) {
		t.Fatalf("повтор изменил файл, хотя значения те же")
	}
	if n != 0 {
		t.Fatalf("повтор отчитался «записано %d», а файл не изменился — счёт идёт по совпавшим записям, а не по изменившимся", n)
	}
}

// Отрицательный контроль к предыдущему: счёт не должен схлопнуться в ноль
// вообще. Изменившаяся запись обязана считаться, даже когда рядом лежат
// совпадающие.
func TestApplyDrift_countsTheOneThatActuallyChanged(t *testing.T) {
	path := driftCatalog(t)
	checked := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	first := []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: checked, Code: 200},
		{EntryID: 2, CheckedAt: checked, Code: 200},
	}
	if _, err := catalogjson.ApplyDrift(path, first); err != nil {
		t.Fatalf("ApplyDrift: %v", err)
	}

	// у записи 2 меняется код, у записи 1 — ничего
	second := []catalogjson.DriftRecord{
		{EntryID: 1, CheckedAt: checked, Code: 200},
		{EntryID: 2, CheckedAt: checked, Code: 404},
	}
	n, err := catalogjson.ApplyDrift(path, second)
	if err != nil {
		t.Fatalf("ApplyDrift (второй): %v", err)
	}
	if n != 1 {
		t.Fatalf("записано %d, ожидалась 1 — изменилась ровно одна запись", n)
	}
}
