package main

import "errors"

var (
	errQRTooLong    = errors.New("текст не помещается в QR")
	errQRUnreadable = errors.New("нарисованный QR не читается — привязка не показана")
)

// qrScale — точек экрана на модуль QR: адрес с токеном и ключом — версия 7-8
// (45-49 модулей), картинка ~400 точек.
const qrScale = 8

// qrPNG draws text as a QR code and reads it back before giving it out:
// окно не покажет телефону код, который не читается или читается не тем.
func qrPNG(text string) ([]byte, error) {
	png, err := qrEncode(text, qrScale)
	if err != nil {
		return nil, err
	}
	if got, err := qrDecode(png); err != nil || got != text {
		return nil, errQRUnreadable
	}
	return png, nil
}
