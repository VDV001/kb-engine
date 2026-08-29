package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidRunRecord is returned when a run record breaks its invariants.
var ErrInvalidRunRecord = errors.New("invalid run record")

// maxExitCode is the largest value a process exit status can carry.
const maxExitCode = 255

// MaxRunReasonRunes caps how much of the failure message the journal keeps.
//
// Обрезка по РУНАМ, а не по байтам: сообщения движка кириллические, и разрез
// по байтам оставил бы половину символа — строка перестала бы быть валидным
// UTF-8 ровно в том файле, который читают инвариантами.
//
// Двести — суждение, а не замер: столько занимает первая строка самых длинных
// сообщений движка вместе с именем команды. ponytail: потолок — длинные
// сообщения теряют хвост; путь наверх — хранить целиком, если окажется, что
// хвост нужен для разбора.
const MaxRunReasonRunes = 200

// RunFailure is what can be said about a non-zero run WITHOUT reading its
// message. Класс выводится из кода возврата и потому безопасен для показа:
// текст причины несёт имена счетов владельца, класс — нет.
//
// ⚠️ Соответствие кодов классам — ЗАМЕР, а не гарантия. 29.08.2026 по живому
// журналу (1917 прогонов, 287 отказов): код 2 давали разбор флагов
// стандартной библиотеки и неизвестная команда, код 1 — отказы самого движка
// (незнакомый счёт, повтор записи, найденное расхождение). Оба класса
// воспроизведены на копиях. Команда, которая начнёт возвращать 1 при поломке,
// попадёт в «отказала» — это ограничение названо здесь, а не спрятано.
type RunFailure string

// Классы отказа.
const (
	// RunFailureNone — прогон завершился успехом, класса нет.
	RunFailureNone RunFailure = ""
	// RunFailureRefused — команда отработала и отказала. Чаще всего это
	// сработавшая защита, а не поломка.
	RunFailureRefused RunFailure = "команда отказала"
	// RunFailureUsage — команду позвали неверно: забытый или незнакомый флаг.
	RunFailureUsage RunFailure = "ошибка вызова"
	// RunFailureOther — код, о котором замер ничего не говорит.
	RunFailureOther RunFailure = "иной отказ"
)

// RunRecord is one execution of a command: what was run, when, how long it
// took and what it returned.
//
// Записи существуют ради инвариантов — проверок над тем, что движок делал на
// самом деле, а не над тем, что он о себе сообщил. Отсюда жёсткость
// конструктора: проверка, считающая по времени, не может защищаться от записи
// с моментом в будущем, поэтому такая запись не должна появляться вовсе.
type RunRecord struct {
	command   string
	args      []string
	startedAt time.Time
	took      time.Duration
	exitCode  int
	reason    string
}

// NewRunRecord validates a run and returns it. now is passed in rather than
// read from the clock: the domain has no clock of its own.
func NewRunRecord(
	command string,
	args []string,
	startedAt time.Time,
	took time.Duration,
	exitCode int,
	now time.Time,
) (RunRecord, error) {
	return NewRunRecordWithReason(command, args, startedAt, took, exitCode, "", now)
}

// NewRunRecordWithReason validates a run that also carries why it failed.
//
// Инвариант: причина существует ТОЛЬКО у отказа. У успеха её нет по
// конструкции, и запись «успех с причиной» описывала бы событие, которого не
// бывает, — поэтому она отвергается, а не молча очищается.
func NewRunRecordWithReason(
	command string,
	args []string,
	startedAt time.Time,
	took time.Duration,
	exitCode int,
	reason string,
	now time.Time,
) (RunRecord, error) {
	reason = strings.TrimSpace(reason)
	if exitCode == 0 && reason != "" {
		return RunRecord{}, fmt.Errorf("%w: exit code 0 carries a reason (%q) — success has none",
			ErrInvalidRunRecord, reason)
	}
	if n := utf8.RuneCountInString(reason); n > MaxRunReasonRunes {
		reason = string([]rune(reason)[:MaxRunReasonRunes])
	}
	if strings.TrimSpace(command) == "" {
		return RunRecord{}, fmt.Errorf("%w: command is empty", ErrInvalidRunRecord)
	}
	if startedAt.After(now) {
		return RunRecord{}, fmt.Errorf("%w: started at %s, which is after now (%s)",
			ErrInvalidRunRecord, startedAt.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if took < 0 {
		return RunRecord{}, fmt.Errorf("%w: took %s, which is negative", ErrInvalidRunRecord, took)
	}
	if exitCode < 0 || exitCode > maxExitCode {
		return RunRecord{}, fmt.Errorf("%w: exit code %d is outside 0..%d",
			ErrInvalidRunRecord, exitCode, maxExitCode)
	}
	return RunRecord{
		command:   command,
		args:      slices.Clone(args),
		startedAt: startedAt,
		took:      took,
		exitCode:  exitCode,
		reason:    reason,
	}, nil
}

// Command is the verb that was run.
func (r RunRecord) Command() string { return r.command }

// Args are the arguments it was given. The caller gets a copy: a journal that
// can be edited through its own getter is not a record of anything.
func (r RunRecord) Args() []string { return slices.Clone(r.args) }

// StartedAt is when the run began.
func (r RunRecord) StartedAt() time.Time { return r.startedAt }

// Took is how long it ran.
func (r RunRecord) Took() time.Duration { return r.took }

// ExitCode is the process exit status it returned.
func (r RunRecord) ExitCode() int { return r.exitCode }

// Reason is the first line of what the command said when it failed. Пустая
// строка означает «причину не записывали» — у записей, сделанных до появления
// поля, отличить это от «причины не было» нечем, и различитель здесь не
// выдумывается.
//
// ⚠️ Текст приходит из stderr и может нести имена счетов и пути владельца.
// Хранение и показ — разные вопросы: наружу идёт Failure(), а не Reason().
func (r RunRecord) Reason() string { return r.reason }

// Failure names what can be said about the run without reading its message.
func (r RunRecord) Failure() RunFailure {
	switch r.exitCode {
	case 0:
		return RunFailureNone
	case 1:
		return RunFailureRefused
	case 2:
		return RunFailureUsage
	default:
		return RunFailureOther
	}
}

// Succeeded reports whether the run returned zero. Invariants ask this far
// more often than they ask for the number itself.
func (r RunRecord) Succeeded() bool { return r.exitCode == 0 }
