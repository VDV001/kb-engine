//go:build !(darwin && cgo)

package main

// Под Windows и без cgo QR пока нет: окно скажет об этом словами
// (ponytail: потолок — привязка только с Мака; путь — свой рисовальщик
// под Windows на этапе 5 #622).
func qrSupported() bool { return false }

func qrEncode(string, int) ([]byte, error) { return nil, errQRUnsupported }

func qrDecode([]byte) (string, error) { return "", errQRUnsupported }
