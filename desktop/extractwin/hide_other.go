//go:build !windows

package main

import "os/exec"

// Помощник собирается под Windows; на другой ОС окна консоли нет.
func hideWindow(*exec.Cmd) {}
