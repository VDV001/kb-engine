//go:build !windows

package main

// Команды открытия файла macOS: Finder и Quick Look. Под Windows —
// open_cmd_windows.go.

func openCmd(path string) []string    { return []string{"open", path} }
func revealCmd(path string) []string  { return []string{"open", "-R", path} }
func previewCmd(path string) []string { return []string{"qlmanage", "-p", path} }
func revealResult(err error) error    { return err }
