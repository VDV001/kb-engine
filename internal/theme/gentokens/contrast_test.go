package main

import (
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/theme"
)

// Every pair of tokens that ends up as text on a background, checked in both
// themes.
//
// This exists because fixing one unreadable label produced its mirror image.
// The spotlight card inverts between themes — black in light, paper in dark —
// while the token for its small print did not, so a colour with a ratio of 16
// against one background had 2.7 against the other. Neither is visible in a
// review of the theme you happen to be running.
//
// 4.5 is the AA threshold for small text, and every pair here is small text.
const minContrast = 4.5

func TestTokenPairsAreReadable(t *testing.T) {
	d, err := load(filepath.Join("..", "..", "..", "design", "tokens.json"))
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}

	pairs := []struct{ text, background string }{
		{"on-surface", "bg"},
		{"on-surface-variant", "bg"},
		{"on-surface", "surface-low"},
		{"on-surface-variant", "surface-low"},
		{"on-primary", "primary"},
		{"kpi-3-text", "kpi-3-bg"},
		{"kpi-3-sub", "kpi-3-bg"},
		{"card-spotlight-text", "card-spotlight-bg"},
		{"on-error-container", "error-container"},
		{"tag-text-1", "tag-bg-1"},
		{"tag-text-2", "tag-bg-2"},
		{"tag-text-3", "tag-bg-3"},
		{"tag-text-4", "tag-bg-4"},
		// Статусы каталога — тоже мелкий текст на фоне: подпись рядом с точкой
		// красится тем же тоном. Таблица и сетка стоят на surface-lowest.
		{"status-keep", "surface-lowest"},
		{"status-napodumat", "surface-lowest"},
		{"status-published", "surface-lowest"},
		{"status-review", "surface-lowest"},
	}

	for _, theming := range []struct {
		name string
		pick func(pair) string
	}{
		{"light", func(p pair) string { return p.Light }},
		{"dark", func(p pair) string { return p.Dark }},
	} {
		backdrop := theming.pick(d.Colors[bgRole])
		for _, want := range pairs {
			t.Run(theming.name+"/"+want.text+" on "+want.background, func(t *testing.T) {
				fg, ok := d.Colors[want.text]
				if !ok {
					t.Fatalf("no such role %q", want.text)
				}
				bg, ok := d.Colors[want.background]
				if !ok {
					t.Fatalf("no such role %q", want.background)
				}
				// Через Flatten, потому что часть ролей полупрозрачна: сравнивать
				// надо тот цвет, который человек видит, а не тот, что записан.
				text, err := theme.Flatten(theming.pick(fg), backdrop)
				if err != nil {
					t.Fatalf("%s: %v", want.text, err)
				}
				behind, err := theme.Flatten(theming.pick(bg), backdrop)
				if err != nil {
					t.Fatalf("%s: %v", want.background, err)
				}
				ratio, err := theme.Contrast(text, behind)
				if err != nil {
					t.Fatalf("contrast: %v", err)
				}
				if ratio < minContrast {
					t.Errorf("%s on %s in the %s theme: %s on %s is %.2f, want at least %.1f",
						want.text, want.background, theming.name, text, behind, ratio, minContrast)
				}
			})
		}
	}
}
