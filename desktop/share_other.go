//go:build !darwin

package main

import "errors"

// Сетевые папки подключает mount volume macOS (share_darwin.go). Под Windows
// окно пока говорит об этом словами.
// ponytail: потолок — шары на Windows не подключаются сами; путь — `net use
// \\сервер\папка` и путь UNC вместо /Volumes (#622, этап 5).
var errSharesUnsupported = errors.New("сетевые папки на Windows окно пока не подключает: откройте папку в Проводнике и добавьте ее путь")

func mountShareCmd(string) error { return errSharesUnsupported }

func systemMounts() []mountEntry { return nil }
