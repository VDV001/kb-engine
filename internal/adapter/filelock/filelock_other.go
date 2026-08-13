//go:build !unix

package filelock

import "os"

// ponytail: на не-unix замка нет — flock там отсутствует, а LockFileEx лежит в
// golang.org/x/sys/windows, то есть пятой прямой зависимостью ради платформы, на
// которой этот инструмент пока никто не запускает. Потолок назван честно: на
// Windows два одновременных писателя журнала по-прежнему могут потерять запись.
// Путь апгрейда — x/sys/windows LockFileEx с теми же сигнатурами lock/unlock,
// весь остальной код останется как есть.
func lock(*os.File) error { return nil }

func unlock(*os.File) error { return nil }
