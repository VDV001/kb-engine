//go:build windows

package main

import "syscall"

// detachedProcess on Windows has nothing to set: Setsid там нет, а копирование
// в буфер идет через pbcopy, которого в Windows тоже нет, — до очистки дело не
// доходит. Файл нужен, чтобы выпуск собирался под все ОС (07.11 v0.37.0 упал).
func detachedProcess() *syscall.SysProcAttr { return nil }
