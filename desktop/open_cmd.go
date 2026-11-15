//go:build !windows

package main

import "os/exec"

// Команды открытия файла macOS: Finder и Quick Look. Под Windows —
// open_cmd_windows.go.

func openCmd(path string) []string    { return []string{"open", path} }
func revealCmd(path string) []string  { return []string{"open", "-R", path} }
func previewCmd(path string) []string { return []string{"qlmanage", "-p", path} }
func revealResult(err error) error    { return err }

// command builds the process of a window command (open_cmd_windows.go — Windows).
func command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
