package main

import "errors"

var (
	errQRUnsupported = errors.New("QR пока рисуется только на macOS")
	errQRTooLong     = errors.New("текст не помещается в QR")
)

// qrPNG draws text as a QR code.
func qrPNG(string) ([]byte, error) { return nil, nil }

// qrDecode reads a QR code back.
func qrDecode([]byte) (string, error) { return "", nil }
