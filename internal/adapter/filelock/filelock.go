// Package filelock сериализует чтение-с-последующей-записью одного файла между
// процессами.
//
// Атомарная замена файла защищает от обрывка на диске, но не от потерянного
// обновления: два процесса, прочитавшие одно состояние, запишут два разных, и
// победит последний. Для журнала трат это не теория — замер восемью
// одновременными `fin add` дал **одну** строку в файле при восьми командах,
// отчитавшихся успехом.
//
// Замок берется на отдельный файл рядом, а не на сам защищаемый: запись идет
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
	return hold(path, lock, fn)
}

// ErrBusy — замок держит другой процесс дольше срока ctx.
var ErrBusy = errors.New("file is locked by another process")

// pollEvery — как часто WithContext пробует замок.
const pollEvery = 20 * time.Millisecond

// WithContext is With that gives up when ctx ends: читатель, которому нельзя
// висеть (MCP-сервер ждет агент), получает ErrBusy по истечении срока, а при
// отмене — ошибку ctx без ErrBusy, и fn не зовется.
//
// ponytail: замок пробуется раз в pollEvery, а не ждется в ядре: блокирующий
// flock не отменить, и брошенная горутина взяла бы замок позже и держала его.
// Потолок — до pollEvery лишней задержки после освобождения. Путь апгрейда,
// если задержка станет заметна, — меньший pollEvery: событий flock не шлет.
func WithContext(ctx context.Context, path string, fn func() error) error {
	return hold(path, func(f *os.File) error { return poll(ctx, f) }, fn)
}

// poll tries the lock every pollEvery until it is taken or ctx ends.
func poll(ctx context.Context, f *os.File) error {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		ok, err := tryLock(f)
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// hold opens the lock file, takes the lock with acquire and runs fn under it.
func hold(path string, acquire func(*os.File) error, fn func() error) error {
	f, err := os.OpenFile(path+Suffix, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := acquire(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() { _ = unlock(f) }()

	return fn()
}
