package usagejsonl_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/usagejsonl"
	"github.com/daniil/kb-engine/internal/domain"
)

func call(t *testing.T) domain.ModelCall {
	t.Helper()
	c, err := domain.NewModelCall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), domain.CallSearch, "anthropic/claude-sonnet-5.5", 3100, 180, true, 900*time.Millisecond, 4*time.Second, false)
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
	for _, k := range []string{"failed", "uncounted"} {
		if _, ok := line[k]; ok {
			t.Errorf("удачный вызов со счетом, а в строке %s: %s", k, raw)
		}
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

// Сервер не прислал счет — это не «ноль токенов»: строка так и говорит.
func TestJournal_Uncounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	c, _ := domain.NewModelCall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), domain.CallAnswer, "qwen3", 0, 0, false, time.Second, time.Second, false)
	if err := (usagejsonl.Journal{Path: path}).Record(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var line map[string]any
	_ = json.Unmarshal(raw, &line)
	if line["uncounted"] != true {
		t.Fatalf("строка без пометки: %s", raw)
	}
}

// Цена вызова от шлюза переживает журнал: пишется пикодолларами и читается
// обратно; у обычного вызова поля цены в строке нет.
func TestJournal_Billed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	c, err := domain.NewBilledModelCall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), domain.CallListen, "openai/gpt-4o-mini-transcribe", 96_250_000, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(c); err != nil {
		t.Fatal(err)
	}
	if err := j.Record(call(t)); err != nil {
		t.Fatal(err)
	}
	calls, bad, err := j.Load()
	if err != nil || bad != 0 || len(calls) != 2 {
		t.Fatalf("прочитано %d, битых %d, %v", len(calls), bad, err)
	}
	if !calls[0].Billed() || calls[0].Cost() != 96_250_000 || calls[1].Billed() {
		t.Fatalf("цена не пережила журнал: %+v", calls)
	}
	f, _ := os.Open(path)
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Scan()
	sc.Scan()
	var plain map[string]any
	_ = json.Unmarshal(sc.Bytes(), &plain)
	if _, ok := plain["cost_pico"]; ok {
		t.Fatalf("у вызова без цены поле цены: %s", sc.Bytes())
	}
}

// Чат вызова переживает запись и чтение; вызов вне чата не получает пустого поля.
func TestJournal_ChatRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	if err := j.Record(call(t).WithChat("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if err := j.Record(call(t)); err != nil {
		t.Fatal(err)
	}
	calls, bad, err := j.Load()
	if err != nil || bad != 0 || len(calls) != 2 {
		t.Fatalf("Load = %d вызовов, %d битых, %v", len(calls), bad, err)
	}
	if calls[0].Chat() != "0123456789abcdef" || calls[1].Chat() != "" {
		t.Fatalf("чаты: %q, %q", calls[0].Chat(), calls[1].Chat())
	}
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if !strings.Contains(lines[0], `"chat":"0123456789abcdef"`) || strings.Contains(lines[1], `"chat"`) {
		t.Fatalf("строки журнала:\n%s", raw)
	}
}
