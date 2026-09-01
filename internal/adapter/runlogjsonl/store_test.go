package runlogjsonl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/domain"
)

// clock — момент, от которого домен считает «старт в будущем».
func clock() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) }

func record(t *testing.T, cmd string, args []string, exit int) domain.RunRecord {
	t.Helper()
	r, err := domain.NewRunRecord(cmd, args, clock().Add(-time.Minute), 250*time.Millisecond, exit, clock())
	if err != nil {
		t.Fatalf("NewRunRecord: %v", err)
	}
	return r
}

func TestDefaultPath(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
		err  bool
	}{
		{
			name: "KBENGINE_RUNLOG решает раньше всех",
			env:  map[string]string{"KBENGINE_RUNLOG": "/tmp/own.jsonl", "XDG_STATE_HOME": "/xdg", "HOME": "/home/d"},
			want: "/tmp/own.jsonl",
		},
		{
			name: "XDG_STATE_HOME, когда своего пути не задали",
			env:  map[string]string{"XDG_STATE_HOME": "/xdg", "HOME": "/home/d"},
			want: "/xdg/kbengine/runs.jsonl",
		},
		{
			name: "иначе ~/.local/state",
			env:  map[string]string{"HOME": "/home/d"},
			want: "/home/d/.local/state/kbengine/runs.jsonl",
		},
		{
			name: "без дома пути нет — молчать нельзя",
			env:  map[string]string{},
			err:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runlogjsonl.DefaultPath(func(k string) string { return tt.env[k] })
			if tt.err {
				if err == nil {
					t.Fatalf("ждали ошибку, получили путь %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DefaultPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("путь = %q, ждали %q", got, tt.want)
			}
		})
	}
}

func TestAppend_createsDirectoryAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "kbengine", "runs.jsonl")
	if err := runlogjsonl.Append(path, record(t, "audit", []string{"--check", "all"}, 0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл не создан: %v", err)
	}
	if n := strings.Count(strings.TrimSpace(string(raw)), "\n"); n != 0 {
		t.Errorf("строк в файле %d, ждали одну", n+1)
	}
	if !strings.Contains(string(raw), `"audit"`) {
		t.Errorf("в строке нет имени команды: %s", raw)
	}
}

func TestAppend_doesNotOverwritePreviousRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := runlogjsonl.Append(path, record(t, "audit", nil, 0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := runlogjsonl.Append(path, record(t, "drift", nil, 1)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs, unreadable, err := runlogjsonl.Load(path, clock)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if unreadable != 0 {
		t.Errorf("нечитаемых строк %d, ждали 0", unreadable)
	}
	if len(recs) != 2 {
		t.Fatalf("записей %d, ждали 2 — дозапись затёрла прошлое", len(recs))
	}
	if recs[0].Command() != "audit" || recs[1].Command() != "drift" {
		t.Errorf("порядок записей: %q, %q", recs[0].Command(), recs[1].Command())
	}
}

func TestLoad_roundTripsEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	want := record(t, "fin", []string{"add", "--amount", "104,99"}, 2)
	if err := runlogjsonl.Append(path, want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs, _, err := runlogjsonl.Load(path, clock)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("записей %d, ждали 1", len(recs))
	}
	got := recs[0]
	if got.Command() != want.Command() {
		t.Errorf("команда = %q, ждали %q", got.Command(), want.Command())
	}
	if strings.Join(got.Args(), " ") != strings.Join(want.Args(), " ") {
		t.Errorf("аргументы = %v, ждали %v", got.Args(), want.Args())
	}
	if !got.StartedAt().Equal(want.StartedAt()) {
		t.Errorf("старт = %s, ждали %s", got.StartedAt(), want.StartedAt())
	}
	if got.Took() != want.Took() {
		t.Errorf("длительность = %s, ждали %s", got.Took(), want.Took())
	}
	if got.ExitCode() != want.ExitCode() {
		t.Errorf("код = %d, ждали %d", got.ExitCode(), want.ExitCode())
	}
}

// Отсутствующий файл — не ошибка, в отличие от журнала трат: движок мог
// никогда не запускаться, и это законный ответ «прогонов не было».
func TestLoad_missingFileIsAnEmptyJournal(t *testing.T) {
	recs, unreadable, err := runlogjsonl.Load(filepath.Join(t.TempDir(), "нет.jsonl"), clock)
	if err != nil {
		t.Fatalf("отсутствие файла объявлено ошибкой: %v", err)
	}
	if len(recs) != 0 || unreadable != 0 {
		t.Errorf("записей %d, нечитаемых %d — ждали пустой журнал", len(recs), unreadable)
	}
}

// Битая строка не должна глушить журнал целиком: одна оборванная запись
// сделала бы немыми все инварианты. Но и молчать о ней нельзя — её считают.
func TestLoad_countsUnreadableLinesInsteadOfGivingUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := runlogjsonl.Append(path, record(t, "audit", nil, 0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Обрывок и запись, нарушающая инвариант домена: код возврата вне диапазона
	// процесса. Обе не должны стать записями.
	if _, err := f.WriteString("{\"command\":\"audit\",\n" +
		`{"command":"drift","args":[],"started_at":"2026-08-19T11:00:00Z","took_ms":10,"exit_code":999}` + "\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	recs, unreadable, err := runlogjsonl.Load(path, clock)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("прочитано записей %d, ждали 1 целую", len(recs))
	}
	if unreadable != 2 {
		t.Errorf("нечитаемых %d, ждали 2 — обрывок и нарушение инварианта", unreadable)
	}
}

// --- #328: причина отказа переживает круг «записали → прочитали» ------------

func TestAppendLoad_carriesFailureReason(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.jsonl")
	now := time.Now().UTC().Truncate(time.Millisecond)

	rec, err := domain.NewRunRecordWithReason("fin", []string{"balance"},
		now.Add(-time.Second), time.Second, 1, "fin balance: --amount is required", now)
	if err != nil {
		t.Fatalf("NewRunRecordWithReason: %v", err)
	}
	if err := runlogjsonl.Append(path, rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, bad, err := runlogjsonl.Load(path, func() time.Time { return now })
	if err != nil || bad != 0 || len(got) != 1 {
		t.Fatalf("Load: %v, нечитаемых %d, записей %d", err, bad, len(got))
	}
	if got[0].Reason() != "fin balance: --amount is required" {
		t.Errorf("Reason() после круга = %q", got[0].Reason())
	}
}

// Строка, записанная ДО появления поля, читается и даёт пустую причину.
// ⚠️ Это «причину не записывали», а не «причины не было» — различить их по
// такой строке нечем, и проверка существует, чтобы это было сказано вслух.
func TestLoad_oldLineWithoutReason(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.jsonl")
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := `{"command":"fin","args":["sync"],"started_at":"` +
		now.Add(-time.Second).Format(time.RFC3339Nano) + `","took_ms":12,"exit_code":1}`
	if err := os.WriteFile(path, []byte(old+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, bad, err := runlogjsonl.Load(path, func() time.Time { return now })
	if err != nil || bad != 0 || len(got) != 1 {
		t.Fatalf("Load: %v, нечитаемых %d, записей %d", err, bad, len(got))
	}
	if got[0].Reason() != "" {
		t.Errorf("старая строка дала причину %q, ждали пустую", got[0].Reason())
	}
	if got[0].Failure() != domain.RunFailureRefused {
		t.Errorf("класс у старой строки = %q, ждали вывод из кода возврата", got[0].Failure())
	}
}
