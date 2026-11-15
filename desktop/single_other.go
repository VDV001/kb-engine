//go:build !windows

package main

import "github.com/wailsapp/wails/v2/pkg/options"

// singleInstance: на Маке второй запуск гасит LaunchServices (menubar_windows.go — Windows).
func singleInstance() *options.SingleInstanceLock { return nil }
