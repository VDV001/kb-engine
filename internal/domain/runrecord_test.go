package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewRunRecord(t *testing.T) {
	now := time.Date(2026, 8, 18, 21, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		command string
		args    []string
		started time.Time
		took    time.Duration
		code    int
		wantErr error
	}{
		{
			name:    "обычный прогон",
			command: "fin",
			args:    []string{"add", "--amount", "418"},
			started: now.Add(-time.Second),
			took:    900 * time.Millisecond,
			code:    0,
		},
		{
			name:    "ненулевой код возврата — законная запись",
			command: "audit",
			args:    []string{"--check", "all"},
			started: now.Add(-time.Second),
			took:    time.Second,
			code:    1,
		},
		{
			name:    "команда без аргументов",
			command: "version",
			started: now.Add(-time.Second),
			took:    time.Millisecond,
			code:    0,
		},
		{
			name:    "пустая команда отвергается",
			command: "",
			started: now.Add(-time.Second),
			took:    time.Millisecond,
			code:    0,
			wantErr: domain.ErrInvalidRunRecord,
		},
		{
			name:    "команда из пробелов отвергается",
			command: "   ",
			started: now.Add(-time.Second),
			took:    time.Millisecond,
			code:    0,
			wantErr: domain.ErrInvalidRunRecord,
		},
		{
			// Момент в будущем означает сбой часов или подделку журнала.
			// Инварианты считают по времени, поэтому такую запись принимать
			// нельзя: она встанет в порядке впереди всего настоящего.
			name:    "старт в будущем отвергается",
			command: "audit",
			started: now.Add(time.Minute),
			took:    time.Millisecond,
			code:    0,
			wantErr: domain.ErrInvalidRunRecord,
		},
		{
			name:    "отрицательная длительность отвергается",
			command: "audit",
			started: now.Add(-time.Second),
			took:    -time.Millisecond,
			code:    0,
			wantErr: domain.ErrInvalidRunRecord,
		},
		{
			name:    "код возврата вне диапазона процесса отвергается",
			command: "audit",
			started: now.Add(-time.Second),
			took:    time.Millisecond,
			code:    256,
			wantErr: domain.ErrInvalidRunRecord,
		},
		{
			name:    "отрицательный код возврата отвергается",
			command: "audit",
			started: now.Add(-time.Second),
			took:    time.Millisecond,
			code:    -1,
			wantErr: domain.ErrInvalidRunRecord,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewRunRecord(tt.command, tt.args, tt.started, tt.took, tt.code, now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Command() != tt.command {
				t.Errorf("Command() = %q, want %q", got.Command(), tt.command)
			}
			if got.ExitCode() != tt.code {
				t.Errorf("ExitCode() = %d, want %d", got.ExitCode(), tt.code)
			}
			if !got.StartedAt().Equal(tt.started) {
				t.Errorf("StartedAt() = %v, want %v", got.StartedAt(), tt.started)
			}
			if got.Took() != tt.took {
				t.Errorf("Took() = %v, want %v", got.Took(), tt.took)
			}
		})
	}
}

// Аргументы копируются, иначе вызывающий может изменить их после создания
// записи — и журнал расскажет не о том прогоне, который был.
func TestRunRecord_argsAreCopied(t *testing.T) {
	now := time.Date(2026, 8, 18, 21, 0, 0, 0, time.UTC)
	args := []string{"add", "--amount", "418"}

	rec, err := domain.NewRunRecord("fin", args, now.Add(-time.Second), time.Second, 0, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args[2] = "9999"

	if got := rec.Args(); got[2] != "418" {
		t.Fatalf("Args()[2] = %q, want %q — запись держит копию, а не ссылку", got[2], "418")
	}

	rec.Args()[2] = "7777"
	if got := rec.Args(); got[2] != "418" {
		t.Fatalf("Args()[2] = %q после правки выданного среза — геттер обязан отдавать копию", got[2])
	}
}

// Успех — это ровно нулевой код. Отдельный предикат, потому что инварианты
// спрашивают об этом чаще, чем о самом числе.
func TestRunRecord_succeeded(t *testing.T) {
	now := time.Date(2026, 8, 18, 21, 0, 0, 0, time.UTC)

	ok, err := domain.NewRunRecord("audit", nil, now.Add(-time.Second), time.Second, 0, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok.Succeeded() {
		t.Error("Succeeded() = false при коде 0")
	}

	bad, err := domain.NewRunRecord("audit", nil, now.Add(-time.Second), time.Second, 2, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bad.Succeeded() {
		t.Error("Succeeded() = true при коде 2")
	}
}
