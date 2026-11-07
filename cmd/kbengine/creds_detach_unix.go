//go:build !windows

package main

import "syscall"

// detachedProcess starts the clipboard cleaner in its own session: он
// переживает закрытие терминала и стирает пароль через 30 с.
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
