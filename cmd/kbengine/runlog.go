package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/domain"
)

// runLogged runs a command and records the run in the journal.
//
// Обёртка стоит вокруг ЕДИНСТВЕННОГО диспетчера, а не внутри команд: через
// runWithStdin проходит каждая команда, включая неизвестную, поэтому новая
// команда не сможет забыть про журнал — в отличие от списка, который
// поддерживают руками.
//
// ⚠️ Тесты остальных команд зовут run/runWithStdin напрямую и журнала не
// пишут. Так и задумано — иначе прогон набора засорял бы журнал разработчика,
// — но значит, что путь записи покрыт только тестами рядом с этим файлом.
func runLogged(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	startedAt := time.Now()
	code := runWithStdin(args, stdin, stdout, stderr)
	if err := recordRun(args, startedAt, time.Now(), code); err != nil {
		// Журнал — наблюдатель, а не участник: его поломка не имеет права
		// изменить исход команды. Но и молчать нельзя, иначе пустой журнал
		// читается как «прогонов не было», а это худший ответ для проверки,
		// заведённой ради вопроса «запускали ли».
		fmt.Fprintf(stderr, "kbengine: журнал прогонов не записан: %v\n", err)
	}
	return code
}

// recordRun turns one finished run into a journal line.
//
// Вызов без команды вовсе (`kbengine` без аргументов) записью не становится:
// домен отвергает пустое имя, и выдумывать его ради строки в журнале значило
// бы записать событие, которого не было.
func recordRun(args []string, startedAt, finishedAt time.Time, code int) error {
	if len(args) == 0 {
		return nil
	}
	path, err := runlogjsonl.DefaultPath(os.Getenv)
	if err != nil {
		return err
	}
	rec, err := domain.NewRunRecord(args[0], args[1:], startedAt,
		finishedAt.Sub(startedAt), code, finishedAt)
	if err != nil {
		return err
	}
	return runlogjsonl.Append(path, rec)
}
