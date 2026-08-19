package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
)

// Журнал прогонов существует ради вопроса «запускали ли», поэтому проверяется
// живой путь целиком: обёртка вокруг диспетчера, а не функция записи отдельно.
// Записывать должна КАЖДАЯ команда, включая ту, которой нет.
func TestRunLogged_recordsEveryRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantExit int
	}{
		{name: "известная команда", args: []string{"version"}, wantCmd: "version", wantExit: 0},
		{name: "неизвестная команда тоже прогон", args: []string{"такой-нет"}, wantCmd: "такой-нет", wantExit: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runs.jsonl")
			t.Setenv("KBENGINE_RUNLOG", path)

			var out, errb bytes.Buffer
			code := runLogged(tt.args, strings.NewReader(""), &out, &errb)
			if code != tt.wantExit {
				t.Fatalf("код = %d, ждали %d (stderr: %s)", code, tt.wantExit, errb.String())
			}

			recs, unreadable, err := runlogjsonl.Load(path, time.Now)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if unreadable != 0 {
				t.Errorf("нечитаемых строк %d, ждали 0", unreadable)
			}
			if len(recs) != 1 {
				t.Fatalf("записей в журнале %d, ждали 1", len(recs))
			}
			if recs[0].Command() != tt.wantCmd {
				t.Errorf("команда = %q, ждали %q", recs[0].Command(), tt.wantCmd)
			}
			if recs[0].ExitCode() != tt.wantExit {
				t.Errorf("код в журнале = %d, ждали %d", recs[0].ExitCode(), tt.wantExit)
			}
		})
	}
}

// Аргументы попадают в журнал целиком (решение владельца 19.08.2026): без них
// `audit --check all` неотличим от `audit --check links`, а первый инвариант
// стоит именно на этом различении.
func TestRunLogged_recordsArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("KBENGINE_RUNLOG", path)

	var out, errb bytes.Buffer
	runLogged([]string{"такой-нет", "--check", "all"}, strings.NewReader(""), &out, &errb)

	recs, _, err := runlogjsonl.Load(path, time.Now)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("записей %d, ждали 1", len(recs))
	}
	if got := strings.Join(recs[0].Args(), " "); got != "--check all" {
		t.Errorf("аргументы = %q, ждали %q", got, "--check all")
	}
}

// Журнал — наблюдатель, а не участник: его поломка не имеет права изменить
// код возврата команды. Но и молчать о ней нельзя, иначе пустой журнал
// читается как «прогонов не было».
func TestRunLogged_journalFailureDoesNotChangeExitCodeButIsSaidAloud(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "занято")
	if err := os.WriteFile(blocker, []byte("не каталог"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Путь ведёт ВНУТРЬ обычного файла — каталог там создать нельзя.
	t.Setenv("KBENGINE_RUNLOG", filepath.Join(blocker, "runs.jsonl"))

	var out, errb bytes.Buffer
	code := runLogged([]string{"version"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Errorf("код = %d, ждали 0 — поломка журнала изменила исход команды", code)
	}
	if !strings.Contains(out.String(), "kbengine") {
		t.Errorf("команда не отработала: stdout = %q", out.String())
	}
	if !strings.Contains(errb.String(), "журнал прогонов") {
		t.Errorf("о поломке журнала не сказано вслух: stderr = %q", errb.String())
	}
}
