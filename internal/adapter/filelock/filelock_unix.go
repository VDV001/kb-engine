//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// lock ждёт эксклюзивного замка. Ожидание, а не отказ: вторая команда,
// запущенная через секунду после первой, должна записаться, а не сообщить
// человеку, что журнал занят.
func lock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// tryLock takes the lock if it is free, without waiting.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
