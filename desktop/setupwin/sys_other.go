//go:build !windows

package main

import (
	"errors"
	"fmt"
)

// Установщик работает только на Windows; на другой ОС собирается ради тестов.

const (
	mbInfo  = 0
	mbError = 1
)

var errNotWindows = errors.New("установщик DVK работает только на Windows")

func message(title, text string, _ uintptr) uintptr { fmt.Println(title+":", text); return 0 }
func confirm(string, string) bool                   { return false }
func shortcut(string, string, string) error         { return errNotWindows }
func register(string, string) error                 { return errNotWindows }
func unregister()                                   {}
func webView2Installed() bool                       { return false }
func start(string) error                            { return errNotWindows }
func startArgs(string, ...string) error             { return errNotWindows }
func stopRunning(string)                            {}
