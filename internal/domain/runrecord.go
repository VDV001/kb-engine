package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrInvalidRunRecord is returned when a run record breaks its invariants.
var ErrInvalidRunRecord = errors.New("invalid run record")

// maxExitCode is the largest value a process exit status can carry.
const maxExitCode = 255

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

// Succeeded reports whether the run returned zero. Invariants ask this far
// more often than they ask for the number itself.
func (r RunRecord) Succeeded() bool { return r.exitCode == 0 }
