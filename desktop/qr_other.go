//go:build !(darwin && cgo)

package main

import (
	"errors"

	"github.com/daniil/kb-engine/internal/adapter/qrcode"
)

// Под Windows QR рисует свой кодировщик на Go (internal/adapter/qrcode):
// CoreImage нет. Системного декодера тоже нет, поэтому окно не читает код
// обратно; этот же кодировщик читает детектор Apple в тесте на Маке
// (qr_crosscheck_darwin_test.go), каждая длина до 666 байт.
func qrSupported() bool { return true }

const qrReadBack = false

func qrEncode(text string, scale int) ([]byte, error) {
	c, err := qrcode.Encode([]byte(text))
	if errors.Is(err, qrcode.ErrTooLong) {
		return nil, errQRTooLong
	}
	if err != nil {
		return nil, err
	}
	return c.PNG(scale)
}

func qrDecode([]byte) (string, error) { return "", errQRUnsupported }
