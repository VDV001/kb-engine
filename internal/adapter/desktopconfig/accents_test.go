package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func TestAccents_Check(t *testing.T) {
	tests := []struct {
		name string
		a    desktopconfig.Accents
		ok   bool
	}{
		{"пусто — твой выбор, все включены", desktopconfig.Accents{}, true},
		{"все", desktopconfig.Accents{Mode: desktopconfig.AccentsAll}, true},
		{"ничего, свой набор сохранен", desktopconfig.Accents{Mode: desktopconfig.AccentsNone, Off: []string{"pulse"}}, true},
		{"обычные id", desktopconfig.Accents{Off: []string{"tab-ink", "pulse"}}, true},
		{"неизвестный режим", desktopconfig.Accents{Mode: "rainbow"}, false},
		{"пустой id", desktopconfig.Accents{Off: []string{""}}, false},
		{"заглавные и пробел", desktopconfig.Accents{Off: []string{"Tab Ink"}}, false},
		{"длиннее 32", desktopconfig.Accents{Off: []string{"a-very-long-accent-id-that-goes-on-and-on"}}, false},
		{"повтор", desktopconfig.Accents{Off: []string{"pulse", "pulse"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.a.Check()
			if tt.ok != (err == nil) {
				t.Fatalf("err = %v, ok want %v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, desktopconfig.ErrAccent) {
				t.Fatalf("err = %v, want ErrAccent", err)
			}
		})
	}
}

// Оформление переживает запись; испорченный id, повтор и неизвестный режим
// отбрасываются при чтении, а не гасят конфиг: оформление не должно ломать
// источники MCP. Неизвестный, но годный id (его записала новая сборка) хранится.
func TestRead_AccentsRoundTripAndBadDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := desktopconfig.Accents{Mode: desktopconfig.AccentsNone, Off: []string{"pulse", "from-newer-build"}}
	if err := desktopconfig.Write(path, desktopconfig.Config{Accents: want}); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Accents.Mode != want.Mode || !slices.Equal(got.Accents.Off, want.Off) {
		t.Fatalf("Accents = %+v, want %+v", got.Accents, want)
	}
	got, err = desktopconfig.Read(writeRaw(t, `{"sources":[],"accents":{"mode":"rainbow","off":["pulse","","Bad Id","pulse","tip"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Accents.Mode != desktopconfig.AccentsMine || !slices.Equal(got.Accents.Off, []string{"pulse", "tip"}) {
		t.Fatalf("после чистки Accents = %+v", got.Accents)
	}
}
