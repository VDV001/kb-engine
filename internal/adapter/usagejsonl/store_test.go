package usagejsonl_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/usagejsonl"
	"github.com/daniil/kb-engine/internal/domain"
)

func call(t *testing.T) domain.ModelCall {
	t.Helper()
	c, err := domain.NewModelCall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), domain.CallSearch, "anthropic/claude-sonnet-5.5", 3100, 180, 900*time.Millisecond, 4*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Журнал вызовов модели — строка JSON на вызов, файл только для своего
// пользователя (0600); текста вопросов и ответов в нем нет по устройству записи.
func TestJournal_Record(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	if err := j.Record(call(t)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права %v, %v", st.Mode().Perm(), err)
	}
	raw, _ := os.ReadFile(path)
	var line map[string]any
	if err := json.Unmarshal(raw, &line); err != nil {
		t.Fatalf("строка %q: %v", raw, err)
	}
	want := map[string]any{"at": "2026-11-07T12:00:00Z", "purpose": "search", "model": "anthropic/claude-sonnet-5.5", "in": 3100.0, "out": 180.0, "first_ms": 900.0, "took_ms": 4000.0}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, ждали %v", k, line[k], v)
		}
	}
	if _, ok := line["failed"]; ok {
		t.Errorf("удачный вызов с полем failed: %s", raw)
	}
}

// Ответ и разбор портрета пишут одновременно: без замка дозапись теряет строки.
func TestJournal_ConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = j.Record(call(t)) })
	}
	wg.Wait()
	f, _ := os.Open(path)
	defer func() { _ = f.Close() }()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); n++ {
	}
	if n != 8 {
		t.Fatalf("строк %d из 8", n)
	}
}
