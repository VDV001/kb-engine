package main

import (
	"strings"
	"testing"
)

// Установщик, собранный без файлов окна (payload пуст — так он лежит в git),
// не ставит половину, а называет рецепт сборки.
func TestPayloadMissing(t *testing.T) {
	err := checkPayload()
	if err == nil {
		t.Skip("payload собран: just desktop-windows")
	}
	if !strings.Contains(err.Error(), "just desktop-windows") {
		t.Fatalf("ошибка без рецепта: %v", err)
	}
}
