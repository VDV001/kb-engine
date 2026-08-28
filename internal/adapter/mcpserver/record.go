package mcpserver

import "time"

// Заготовка под журнал вызовов: порт объявлен, записи ещё нет.

// Recorder — порт журнала вызовов инструментов.
type Recorder interface {
	RecordCall(tool string, args []string, startedAt time.Time, took time.Duration, exitCode int)
}

// Config — то, что сервер узнаёт снаружи.
type Config struct {
	Version  string
	ViewBase string
	Recorder Recorder
	Now      func() time.Time
}
