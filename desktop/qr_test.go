package main

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// QR привязки читается: картинку CoreImage распознает детектор Apple
// (CIDetector — отдельный от генератора код), и текст тот же до байта.
func TestQRPNGReadsBack(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("QR рисует CoreImage: только macOS (#622, окно под Windows — этап 5)")
	}
	text := "https://kb.example.ru/m/#pair=" + strings.Repeat("t", 43) + "." + strings.Repeat("k", 43)
	png, err := qrPNG(text)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a PNG: % x", png[:min(8, len(png))])
	}
	got, err := qrDecode(png)
	if err != nil || got != text {
		t.Fatalf("decoded %q, %v; want %q", got, err, text)
	}
}

func TestQRPNGTooLong(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("только macOS")
	}
	if _, err := qrPNG(strings.Repeat("x", 4000)); !errors.Is(err, errQRTooLong) {
		t.Errorf("4000 bytes: %v, want errQRTooLong", err)
	}
}
