// Package filelock сериализует чтение-с-последующей-записью одного файла между
// процессами.
//
// Атомарная замена файла защищает от обрывка на диске, но не от потерянного
// обновления: два процесса, прочитавшие одно состояние, запишут два разных, и
// победит последний. Для журнала трат это не теория — замер восемью
// одновременными `fin add` дал **одну** строку в файле при восьми командах,
// отчитавшихся успехом.
//
// Замок берётся на отдельный файл рядом, а не на сам защищаемый: запись идёт
// через временный файл и rename, то есть inode заменяется, и замок на прежнем
// inode не значил бы ничего для того, кто откроет файл после подмены.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Suffix — что дописывается к пути защищаемого файла, чтобы получить имя замка.
const Suffix = ".lock"

// With держит эксклюзивный замок на время работы fn.
//
// Ошибка fn возвращается как есть — замок снимается в любом случае, включая
// панику: иначе один сбой запирал бы журнал до перезапуска машины.
func With(path string, fn func() error) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if err := lock(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() { _ = unlock(f) }()

	return fn()
}

// ErrBusy — замок держит другой процесс дольше срока ctx.
var ErrBusy = errors.New("file is locked by another process")

// pollEvery — как часто WithContext пробует замок.
const pollEvery = 20 * time.Millisecond

// WithContext is With that gives up when ctx ends: читатель, которому нельзя
// висеть (MCP-сервер ждет агент), получает ErrBusy, и fn не зовется.
//
// ponytail: замок пробуется раз в pollEvery, а не ждется в ядре: блокирующий
// flock не отменить, и брошенная горутина взяла бы замок позже и держала его.
// Потолок — до pollEvery лишней задержки после освобождения. Путь апгрейда,
// если задержка станет заметна, — меньший pollEvery: событий flock не шлет.
func WithContext(ctx context.Context, path string, fn func() error) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	for {
		ok, err := tryLock(f)
		if err != nil {
			return fmt.Errorf("lock %s: %w", path, err)
		}
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s: %w", ErrBusy, path, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
	defer func() { _ = unlock(f) }()

	return fn()
}

func open(path string) (*os.File, error) {
	f, err := os.OpenFile(path+Suffix, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return f, nil
}
