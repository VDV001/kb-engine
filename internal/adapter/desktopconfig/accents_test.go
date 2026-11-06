package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func TestCheckAccentsOff(t *testing.T) {
	tests := []struct {
		name string
		list []string
		ok   bool
	}{
		{"пусто — все включены", nil, true},
		{"обычные id", []string{"tab-ink", "pulse"}, true},
		{"пустой id", []string{""}, false},
		{"заглавные и пробел", []string{"Tab Ink"}, false},
		{"длиннее 32", []string{"a-very-long-accent-id-that-goes-on-and-on"}, false},
		{"повтор", []string{"pulse", "pulse"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := desktopconfig.CheckAccentsOff(tt.list)
			if tt.ok != (err == nil) {
				t.Fatalf("err = %v, ok want %v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, desktopconfig.ErrAccent) {
				t.Fatalf("err = %v, want ErrAccent", err)
			}
		})
	}
}

// Список выключенных переживает запись; испорченный id и повтор отбрасываются
// при чтении, а не гасят конфиг: оформление не должно ломать источники MCP.
// Неизвестный, но годный id (его записала новая сборка) хранится.
func TestRead_AccentsOffRoundTripAndBadDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(path, desktopconfig.Config{AccentsOff: []string{"pulse", "from-newer-build"}}); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pulse", "from-newer-build"}; !slices.Equal(got.AccentsOff, want) {
		t.Fatalf("AccentsOff = %v, want %v", got.AccentsOff, want)
	}
	got, err = desktopconfig.Read(writeRaw(t, `{"sources":[],"accents_off":["pulse","","Bad Id","pulse","tip"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pulse", "tip"}; !slices.Equal(got.AccentsOff, want) {
		t.Fatalf("после чистки AccentsOff = %v, want %v", got.AccentsOff, want)
	}
}
