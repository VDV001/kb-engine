//go:build unix

package filelock

import (
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
