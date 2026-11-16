package main

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
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

// command builds the process of a window command. «Показать в Проводнике» —
// своей строкой команды (explorerSelect): кавычки exec Проводник не понимает.
func command(name string, args ...string) *exec.Cmd {
	if p, ok := strings.CutPrefix(strings.Join(args, " "), "/select,"); name == "explorer" && len(args) == 1 && ok {
		cmd := exec.Command(name)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: explorerSelect(p)}
		return cmd
	}
	return exec.Command(name, args...)
}
