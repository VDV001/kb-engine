//go:build !windows

package main

import "os/exec"

// noConsole: консоль есть только у Windows (noconsole_windows.go).
func noConsole(cmd *exec.Cmd) *exec.Cmd { return cmd }
