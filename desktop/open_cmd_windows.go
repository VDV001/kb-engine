package main

import (
	"errors"
	"os/exec"
)

// Команды открытия файла Windows. rundll32 вместо `cmd /c start`: путь идет
// одним аргументом, без разбора кавычек и & оболочкой cmd.

func openCmd(path string) []string   { return []string{"rundll32", "url.dll,FileProtocolHandler", path} }
func revealCmd(path string) []string { return []string{"explorer", "/select," + path} }

// Quick Look на Windows нет: просмотр открывает файл в его программе.
func previewCmd(path string) []string { return openCmd(path) }

// revealResult: explorer возвращает код 1 и тогда, когда папку показал, —
// ненулевой код выхода не ошибка; ошибка только «не запустился».
func revealResult(err error) error {
	if _, exited := errors.AsType[*exec.ExitError](err); exited {
		return nil
	}
	return err
}
