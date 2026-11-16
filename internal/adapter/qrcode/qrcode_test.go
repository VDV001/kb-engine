package qrcode

import (
	"errors"
	"strings"
	"testing"
)

// Таблица блоков коррекции набрана руками — сверяем ее с числом модулей,
// которое считается по формуле из размеров узоров: опечатка в таблице
// разошлась бы с формулой.
func TestECTableMatchesModuleCount(t *testing.T) {
	for ver := 1; ver <= maxVersion; ver++ {
		e := ecM[ver]
		blocks := e.groups[0][0] + e.groups[1][0]
		if got, want := dataCodewords(ver)+blocks*e.ec, rawModules(ver)/8; got != want {
			t.Errorf("версия %d: данных+коррекции %d байт, модулей на %d", ver, got, want)
		}
		if g := e.groups; g[1][0] > 0 && g[1][1] != g[0][1]+1 {
			t.Errorf("версия %d: вторая группа должна быть на байт длиннее первой: %v", ver, g)
		}
	}
}

// Известные значения стандарта: генератор степени 7 (пример ISO 18004, прил. A)
// и размер символа по версии.
func TestKnownValues(t *testing.T) {
	if got := rsGenerator(7); string(got) != string([]byte{0x7F, 0x7A, 0x9A, 0xA4, 0x0B, 0x44, 0x75}) {
		t.Errorf("генератор степени 7 = % X", got)
	}
	for _, tc := range []struct {
		n, size int
	}{{1, 21}, {14, 21}, {15, 25}, {200, 4*maxSize(200) + 17}} {
		c, err := Encode([]byte(strings.Repeat("a", tc.n)))
		if err != nil {
			t.Fatalf("%d байт: %v", tc.n, err)
		}
		if c.Size != tc.size {
			t.Errorf("%d байт: размер %d, ждали %d", tc.n, c.Size, tc.size)
		}
	}
	if _, err := Encode(make([]byte, 667)); !errors.Is(err, ErrTooLong) {
		t.Errorf("667 байт: %v, ждали ErrTooLong", err)
	}
	if _, err := Encode(make([]byte, 666)); err != nil {
		t.Errorf("666 байт: %v", err)
	}
}

func maxSize(n int) int {
	for v := 1; v <= maxVersion; v++ {
		lb := 8
		if v >= 10 {
			lb = 16
		}
		if 4+lb+8*n <= 8*dataCodewords(v) {
			return v
		}
	}
	return 0
}
