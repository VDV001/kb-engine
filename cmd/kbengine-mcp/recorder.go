package main

import (
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
)

// Заготовка под журнал вызовов: запись появится следующим коммитом.

type journalRecorder struct{}

func newJournalRecorder(path string, now func() time.Time, stderr io.Writer) mcpserver.Recorder {
	return journalRecorder{}
}

func (journalRecorder) RecordCall(string, []string, time.Time, time.Duration, int) {}
