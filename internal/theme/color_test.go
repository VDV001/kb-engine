package theme_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/theme"
)

// Nine of the palette's roles are rgba() or transparent. A browser composites
// those against whatever is behind them; a terminal cannot — lipgloss takes one
// solid colour per cell. Flattening them against the theme's background is what
// makes one source of tokens serve both surfaces, so the arithmetic is the
// hinge and gets its own test.
func TestFlatten(t *testing.T) {
	for _, c := range []struct {
		name, value, over, want string
	}{
		// Opaque values pass through untouched: flattening must not quietly
		// redefine the 46 colours that were already solid.
		{"opaque hex", "#99462a", "#fbf9f2", "#99462a"},
		{"opaque hex over dark", "#99462a", "#1a1a1a", "#99462a"},
		{"shorthand hex", "#fff", "#000000", "#ffffff"},

		// Fully transparent is the backdrop, by definition.
		{"transparent keyword", "transparent", "#fbf9f2", "#fbf9f2"},
		{"zero alpha", "rgba(230,126,81,0)", "#1a1a1a", "#1a1a1a"},

		// Full alpha is the colour itself, also by definition.
		{"full alpha", "rgba(230,126,81,1)", "#fbf9f2", "#e67e51"},

		// Half of white over black is the midpoint. 127.5 rounds up, so the
		// expected value is exact rather than a judgement call.
		{"half white over black", "rgba(255,255,255,0.5)", "#000000", "#808080"},
		{"half black over white", "rgba(0,0,0,0.5)", "#ffffff", "#808080"},

		// Channels composite independently: only the red one moves here.
		{"per channel", "rgba(255,0,0,0.5)", "#000000", "#800000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := theme.Flatten(c.value, c.over)
			if err != nil {
				t.Fatalf("Flatten(%q, %q): %v", c.value, c.over, err)
			}
			if got != c.want {
				t.Errorf("Flatten(%q, %q) = %q, want %q", c.value, c.over, got, c.want)
			}
		})
	}
}

// A value the generator cannot resolve has to stop the build. Guessing would
// put a colour nobody chose into the terminal, and the whole point of one
// source is that neither surface invents its own.
func TestFlatten_rejectsWhatItCannotResolve(t *testing.T) {
	for _, c := range []struct{ name, value, over string }{
		{"named colour", "rebeccapurple", "#000000"},
		{"hsl", "hsl(210 40% 50%)", "#000000"},
		{"malformed rgba", "rgba(1,2)", "#000000"},
		{"alpha out of range", "rgba(1,2,3,4)", "#000000"},
		{"channel out of range", "rgba(300,0,0,0.5)", "#000000"},
		{"backdrop not opaque", "rgba(0,0,0,0.5)", "transparent"},
		{"empty", "", "#000000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, err := theme.Flatten(c.value, c.over); err == nil {
				t.Errorf("Flatten(%q, %q) = %q, want an error", c.value, c.over, got)
			}
		})
	}
}

// Compositing moves a channel towards the source colour and never past it —
// the property that catches a swapped operand, which the fixed cases above
// would still pass if both were symmetric.
func TestFlatten_staysBetweenTheColourAndTheBackdrop(t *testing.T) {
	for _, alpha := range []string{"0.1", "0.25", "0.4", "0.75", "0.9"} {
		got, err := theme.Flatten("rgba(230,126,81,"+alpha+")", "#1a1a1a")
		if err != nil {
			t.Fatalf("alpha %s: %v", alpha, err)
		}
		r, g, b, err := theme.ParseHex(got)
		if err != nil {
			t.Fatalf("alpha %s: %v", alpha, err)
		}
		// 0x1a is 26; the source channels are 230, 126, 81 — all above it, so
		// every composite has to land in between.
		for _, ch := range []struct {
			name       string
			got, limit uint8
		}{{"r", r, 230}, {"g", g, 126}, {"b", b, 81}} {
			if ch.got < 26 || ch.got > ch.limit {
				t.Errorf("alpha %s: %s = %d, want between 26 and %d", alpha, ch.name, ch.got, ch.limit)
			}
		}
	}
}
