package main

import (
	"slices"
	"testing"
)

// Список голосов Windows (speak_windows.go) — разбор строк «имя<TAB>культура»
// из System.Speech; проверяется и на Маке.
func TestWindowsVoices(t *testing.T) {
	out := "Microsoft Irina Desktop\tru-RU\r\nMicrosoft Zira Desktop\ten-US\r\nMicrosoft Hazel Desktop\ten-GB\r\n"
	if got := windowsVoices(out, "ru-"); !slices.Equal(got, []string{"Microsoft Irina Desktop"}) {
		t.Fatalf("ru = %q", got)
	}
	if got := windowsVoices(out, "en-GB"); !slices.Equal(got, []string{"Microsoft Hazel Desktop"}) {
		t.Fatalf("en-GB = %q", got)
	}
}
