package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

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
	// Подслушиваем stderr, а не перехватываем: команда пишет туда же, куда и
	// писала, а обёртка запоминает первую строку. Причина стоит в НАЧАЛЕ —
	// движок называет её сразу, а хвост занимают подсказки и перечисления.
	tee := &firstLine{out: stderr}
	code := runWithStdin(args, stdin, stdout, tee)
	reason := ""
	if code != 0 {
		reason = tee.String()
	}
	if err := recordRun(args, startedAt, time.Now(), code, reason); err != nil {
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
func recordRun(args []string, startedAt, finishedAt time.Time, code int, reason string) error {
	if len(args) == 0 {
		return nil
	}
	path, err := runlogjsonl.DefaultPath(os.Getenv)
	if err != nil {
		return err
	}
	rec, err := domain.NewRunRecordWithReason(args[0], args[1:], startedAt,
		finishedAt.Sub(startedAt), code, reason, finishedAt)
	if err != nil {
		return err
	}
	return runlogjsonl.Append(path, rec)
}

// firstLine passes everything through and keeps the first non-empty line.
//
// Обёртка, а не буфер целиком: команда может печатать в stderr сколько угодно,
// и держать всё в памяти ради одной строки незачем. Как только строка найдена,
// writer превращается в чистый проводник.
//
// ⚠️ Пропускает байт в байт: вывод команды не имеет права измениться от того,
// что его подслушивают, — на это стоит отдельный тест.
type firstLine struct {
	out  io.Writer
	buf  []byte
	done bool
}

func (f *firstLine) Write(p []byte) (int, error) {
	if !f.done {
		for _, b := range p {
			if b == '\n' {
				if len(bytes.TrimSpace(f.buf)) > 0 {
					f.done = true
					break
				}
				f.buf = f.buf[:0]
				continue
			}
			// Держим ровно столько, сколько домен согласится хранить: длинная
			// строка всё равно будет обрезана конструктором.
			if utf8.RuneCount(f.buf) < domain.MaxRunReasonRunes*4 {
				f.buf = append(f.buf, b)
			}
		}
	}
	return f.out.Write(p)
}

// String is the first line seen so far, trimmed.
func (f *firstLine) String() string { return strings.TrimSpace(string(f.buf)) }
