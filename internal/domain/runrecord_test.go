package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// --- #328: причина отказа ---------------------------------------------------

// Причина живёт только у отказа. Пустая строка у успешного прогона и
// «причины не записали» здесь один и тот же ответ намеренно: у успеха
// причины НЕТ по конструкции, и различать нечего.
func TestNewRunRecord_reasonOnlyOnFailure(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Second)

	if _, err := domain.NewRunRecordWithReason("audit", nil, started, time.Second, 0, "что-то пошло не так", now); !errors.Is(err, domain.ErrInvalidRunRecord) {
		t.Errorf("причина при нулевом коде: err = %v, ждали ErrInvalidRunRecord", err)
	}

	rec, err := domain.NewRunRecordWithReason("fin", nil, started, time.Second, 1, "fin balance: the workbook does not know this account", now)
	if err != nil {
		t.Fatalf("причина при ненулевом коде: %v", err)
	}
	if rec.Reason() != "fin balance: the workbook does not know this account" {
		t.Errorf("Reason() = %q", rec.Reason())
	}
}

// Причина обрезается по РУНАМ, а не по байтам: сообщения движка кириллические,
// и разрез по байтам оставил бы половину символа — строка перестала бы быть
// валидным UTF-8 ровно в журнале, который читают инвариантами.
func TestNewRunRecord_reasonTrimmedByRunes(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("я", domain.MaxRunReasonRunes+50)
	rec, err := domain.NewRunRecordWithReason("fin", nil, now.Add(-time.Second), time.Second, 1, long, now)
	if err != nil {
		t.Fatalf("NewRunRecordWithReason: %v", err)
	}
	got := rec.Reason()
	if n := utf8.RuneCountInString(got); n != domain.MaxRunReasonRunes {
		t.Errorf("рун в причине = %d, ждали %d", n, domain.MaxRunReasonRunes)
	}
	if !utf8.ValidString(got) {
		t.Error("причина перестала быть валидным UTF-8 — разрез прошёл по байтам")
	}
}

// Старый конструктор остаётся и означает «причину не записывали»: журнал
// содержит записи, сделанные до появления поля, и притворяться, что у них
// причина пустая по смыслу, значило бы соврать в другую сторону.
func TestNewRunRecord_keepsWorkingWithoutReason(t *testing.T) {
	now := time.Now()
	rec, err := domain.NewRunRecord("audit", nil, now.Add(-time.Second), time.Second, 2, now)
	if err != nil {
		t.Fatalf("NewRunRecord: %v", err)
	}
	if rec.Reason() != "" {
		t.Errorf("Reason() = %q, ждали пустую", rec.Reason())
	}
}

// Класс отказа выводится ИЗ КОДА ВОЗВРАТА, и это единственное, что можно
// сказать об отказе, не читая текст. Текст несёт имена счетов владельца и
// наружу не идёт — см. правило пакета runs.
func TestRunRecord_failureKind(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		code int
		want domain.RunFailure
	}{
		{0, domain.RunFailureNone},
		{1, domain.RunFailureRefused},
		{2, domain.RunFailureUsage},
		{7, domain.RunFailureOther},
	} {
		rec, err := domain.NewRunRecord("fin", nil, now.Add(-time.Second), time.Second, tt.code, now)
		if err != nil {
			t.Fatalf("код %d: %v", tt.code, err)
		}
		if got := rec.Failure(); got != tt.want {
			t.Errorf("код %d: Failure() = %q, ждали %q", tt.code, got, tt.want)
		}
	}
}
