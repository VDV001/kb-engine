// Package runlogjsonl stores the journal of engine runs as newline-delimited
// JSON: one run per line, appended, never rewritten.
//
// Журнал живёт НЕ рядом с каталогом, в отличие от остального состояния движка
// (`.balance-state.json`, `.sync-state.json`). Причина в охвате: `version`,
// все `fin *`, `help` и неизвестная команда каталога не знают, и журнал с
// такой дырой не отличал бы «drift не запускали ни разу» от «запускали без
// --catalog» — то есть главный инвариант перестал бы что-либо доказывать.
//
// ⚠️ В строках лежат аргументы команд, а в них — настоящие суммы и места
// владельца (`fin add --amount ... --place ...`), и с #328 туда же попадает
// причина отказа — она приходит из stderr и тоже может назвать счёт. Решение владельца от
// 19.08.2026: хранить целиком, потому что файл лежит вне любого репозитория и
// движком никуда не отправляется. Отсюда правило для всего, что читает журнал:
// НАРУЖУ (в issue, на страницу, в отчёт) значения аргументов не показывать —
// хранение и показ здесь разные вопросы.
package runlogjsonl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/domain"
)

// FileName is the journal's name inside its directory.
const FileName = "runs.jsonl"

// dirName is the directory the journal gets under the state root.
const dirName = "kbengine"

// line is the wire shape of one run.
//
// Длительность — целые миллисекунды, а не строка `time.Duration`: журнал
// читают арифметикой (медиана, выброс), и число не требует разбора. Момент —
// RFC3339 с наносекундами, потому что два прогона подряд укладываются в одну
// секунду, а порядок между ними и есть то, ради чего момент записан.
type line struct {
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	StartedAt string   `json:"started_at"`
	TookMS    int64    `json:"took_ms"`
	ExitCode  int      `json:"exit_code"`
	// Reason — первая строка того, что команда сказала при отказе. Поле
	// `omitempty`: у успеха причины нет по конструкции, и пустая строка в
	// файле означала бы, что её потеряли.
	//
	// ⚠️ Причина попадает под то же правило, что и Args: она приходит из
	// stderr и может нести имена счетов владельца (`fin balance` на
	// незнакомом счёте перечисляет весь лист «Счета»). Хранить целиком —
	// файл лежит вне репозиториев; НАРУЖУ показывать класс отказа, а не
	// текст.
	Reason string `json:"reason,omitempty"`
}

// DefaultPath decides where the journal lives, asking the environment through
// getenv rather than reading it directly — the caller passes os.Getenv.
//
// Ошибка вместо выдуманного пути: журнал, записанный не туда, читается как
// «прогонов не было», а это худший из возможных ответов для проверки, которая
// существует ради вопроса «запускали ли».
func DefaultPath(getenv func(string) string) (string, error) {
	if own := getenv("KBENGINE_RUNLOG"); own != "" {
		return own, nil
	}
	if state := getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, dirName, FileName), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", dirName, FileName), nil
	}
	return "", errors.New("runlog: ни KBENGINE_RUNLOG, ни XDG_STATE_HOME, ни HOME не заданы — некуда писать журнал прогонов")
}

// Append records one run at the end of the journal.
//
// Замок берётся тот же, что у журнала трат: дозапись в конец без него теряет
// строки, и это замерено, а не предположено — восемь одновременных команд дали
// одну строку в файле.
func Append(path string, rec domain.RunRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("runlog: не создать каталог журнала: %w", err)
	}
	raw, err := json.Marshal(encodeLine(rec))
	if err != nil {
		return fmt.Errorf("runlog: не закодировать запись: %w", err)
	}
	return filelock.With(path, func() error {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("runlog: не открыть журнал: %w", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(append(raw, '\n')); err != nil {
			return fmt.Errorf("runlog: не записать прогон: %w", err)
		}
		return f.Close()
	})
}

// Exists reports whether the journal file is on disk.
//
// Вопрос отдельный от Load намеренно: Load отдаёт ноль записей и когда файла
// нет, и когда он пуст, а это разные ответы. «Журнала нет» означает, что
// движок его ни разу не писал — старая сборка или первый запуск; «журнал пуст»
// означает, что писал и команд не было. Свести их в один ноль значит потерять
// ровно то различие, ради которого журнал заведён.
func Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runlog: не проверить журнал: %w", err)
	}
	return true, nil
}

// Load reads the journal, returning the valid records and how many lines it
// could not read.
//
// Нечитаемые строки СЧИТАЮТСЯ, а не глушат чтение и не пропускаются молча:
// одна оборванная запись сделала бы немыми все инварианты сразу, а молчание о
// ней — это ровно то, от чего инварианты и заведены. Отсутствующий файл — не
// ошибка: движок мог никогда не запускаться, и это законный ответ.
func Load(path string, now func() time.Time) ([]domain.RunRecord, int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("runlog: не открыть журнал: %w", err)
	}
	defer func() { _ = f.Close() }()

	var (
		recs       []domain.RunRecord
		unreadable int
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rec, err := decode(sc.Bytes(), now())
		if err != nil {
			unreadable++
			continue
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("runlog: не прочитать журнал: %w", err)
	}
	return recs, unreadable, nil
}

// decode turns one line back into a record, validating it through the domain:
// the journal is data on disk, and only the constructor decides what counts as
// a run.
func decode(raw []byte, now time.Time) (domain.RunRecord, error) {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		return domain.RunRecord{}, err
	}
	startedAt, err := time.Parse(time.RFC3339Nano, l.StartedAt)
	if err != nil {
		return domain.RunRecord{}, err
	}
	return domain.NewRunRecordWithReason(l.Command, l.Args, startedAt,
		time.Duration(l.TookMS)*time.Millisecond, l.ExitCode, l.Reason, now)
}

func encodeLine(r domain.RunRecord) line {
	return line{
		Command:   r.Command(),
		Args:      r.Args(),
		StartedAt: r.StartedAt().Format(time.RFC3339Nano),
		TookMS:    r.Took().Milliseconds(),
		ExitCode:  r.ExitCode(),
		Reason:    r.Reason(),
	}
}
