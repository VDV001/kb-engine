//go:build !devtools

package main

import "testing"

// Инспектор WebKit дает консоль с привязками Go и открытыми секретами любому у
// незаблокированного Мака (ревью #515): только в сборке just desktop-inspect
// (тег devtools), в устанавливаемой — нет. Этот тест собирается без тега.
func TestInspectorOffByDefault(t *testing.T) {
	if inspectable {
		t.Fatal("сборка без тега devtools включает меню и инспектор WebKit")
	}
}
