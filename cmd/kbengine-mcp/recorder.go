package main

import (
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// journalRecorder кладёт вызов инструмента в тот же журнал, который читает
// `kbengine runs`.
//
// Точка сборки, а не адаптер MCP, знает и про файл, и про имя записи: адаптер
// сообщает голое имя инструмента, приставку ставит runs.ToolCommand — одно
// место на писателя и читателя.
type journalRecorder struct {
	path   string
	now    func() time.Time
	stderr io.Writer
}

func newJournalRecorder(path string, now func() time.Time, stderr io.Writer) mcpserver.Recorder {
	return journalRecorder{path: path, now: now, stderr: stderr}
}

// RecordCall записывает вызов и НАЗЫВАЕТ вслух, если не смог.
//
// Молчащий журнал читается как «вызовов не было» — худший ответ для счётчика,
// заведённого ради вопроса «сколько раз агент вообще спрашивал базу». Ошибка при
// этом не меняет ответ агенту: он уже отдан, здесь только наблюдение.
func (j journalRecorder) RecordCall(tool string, args []string, startedAt time.Time, took time.Duration, exitCode int) {
	rec, err := domain.NewRunRecord(runs.ToolCommand(tool), args, startedAt, took, exitCode, j.now())
	if err == nil {
		err = runlogjsonl.Append(j.path, rec)
	}
	if err != nil {
		fmt.Fprintf(j.stderr, "kbengine-mcp: вызов %s не записан в журнал: %v\n", tool, err)
	}
}
