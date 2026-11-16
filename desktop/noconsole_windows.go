package main

import (
	"os/exec"
	"syscall"
)

// noConsole: консольная программа (kbengine.exe), запущенная окном, не
// открывает черное окно консоли поверх DVK.
func noConsole(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}
