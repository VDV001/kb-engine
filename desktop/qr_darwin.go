//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreImage -framework CoreGraphics
#include <stdlib.h>
int kbQRPNG(const char *text, int n, int scale, void **out, int *outLen);
int kbQRDecode(const void *png, int n, char **out);
*/
import "C"

import (
	"errors"
	"unsafe"
)

func qrSupported() bool { return true }

// qrReadBack: на Маке нарисованный код читается обратно детектором Apple.
const qrReadBack = true

// qrEncode draws text with CoreImage (CIQRCodeGenerator, коррекция M): без
// сторонних библиотек, решение владельца 15.11.
func qrEncode(text string, scale int) ([]byte, error) {
	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	var out unsafe.Pointer
	var n C.int
	switch C.kbQRPNG(cs, C.int(len(text)), C.int(scale), &out, &n) {
	case 0:
	case 2:
		return nil, errQRTooLong
	default:
		return nil, errors.New("CoreImage не нарисовал QR")
	}
	defer C.free(out)
	return C.GoBytes(out, n), nil
}

// qrDecode reads a QR code with CIDetector — детектор Apple, отдельный от
// генератора: проверка не закольцована на тот же код.
func qrDecode(png []byte) (string, error) {
	if len(png) == 0 {
		return "", errQRUnreadable
	}
	var out *C.char
	if C.kbQRDecode(unsafe.Pointer(&png[0]), C.int(len(png)), &out) != 0 {
		return "", errQRUnreadable
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), nil
}
