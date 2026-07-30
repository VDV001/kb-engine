// Package theme holds the design tokens the web dashboard and the TUI share,
// and the colour arithmetic that lets one source serve both.
//
// The tokens themselves are generated from design/tokens.json — see
// gentokens. Nothing here should be edited to change a colour; edit the JSON
// and run `go generate ./...`.
package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseHex splits an opaque #rgb or #rrggbb colour into its channels.
func ParseHex(s string) (r, g, b uint8, err error) {
	v, ok := strings.CutPrefix(strings.TrimSpace(s), "#")
	if !ok {
		return 0, 0, 0, fmt.Errorf("colour %q: expected #rgb or #rrggbb", s)
	}
	if len(v) == 3 {
		v = string([]byte{v[0], v[0], v[1], v[1], v[2], v[2]})
	}
	if len(v) != 6 {
		return 0, 0, 0, fmt.Errorf("colour %q: expected 3 or 6 hex digits", s)
	}
	n, err := strconv.ParseUint(v, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("colour %q: %w", s, err)
	}
	return uint8(n >> 16), uint8(n >> 8), uint8(n), nil
}

// Flatten resolves a colour to a solid #rrggbb, compositing it over a backdrop
// when it is translucent.
//
// A browser can layer rgba() over whatever the page puts behind it. A terminal
// cell holds one colour, so the token that reaches lipgloss has to be resolved
// in advance — against the theme's own background, which is what sits behind
// these roles on the page too.
//
// Anything it cannot resolve is an error rather than a guess: one source of
// tokens is only worth having if neither surface invents a colour of its own.
func Flatten(value, over string) (string, error) {
	br, bgc, bb, err := ParseHex(over)
	if err != nil {
		return "", fmt.Errorf("backdrop: %w", err)
	}

	v := strings.TrimSpace(value)
	if v == "transparent" {
		return hex(br, bgc, bb), nil
	}
	if strings.HasPrefix(v, "#") {
		r, g, b, err := ParseHex(v)
		if err != nil {
			return "", err
		}
		return hex(r, g, b), nil
	}

	r, g, b, alpha, err := parseRGBA(v)
	if err != nil {
		return "", err
	}
	mix := func(src, backdrop uint8) uint8 {
		return uint8(math.Round(float64(src)*alpha + float64(backdrop)*(1-alpha)))
	}
	return hex(mix(r, br), mix(g, bgc), mix(b, bb)), nil
}

// parseRGBA reads the one translucent notation the palette uses. Every other
// CSS colour form is rejected rather than approximated — the palette contains
// none, and a form nobody writes is not worth the code to misread it.
func parseRGBA(s string) (r, g, b uint8, alpha float64, err error) {
	body, ok := strings.CutPrefix(s, "rgba(")
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("colour %q: expected #rrggbb, rgba(...) or transparent", s)
	}
	body, ok = strings.CutSuffix(body, ")")
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("colour %q: missing closing bracket", s)
	}

	parts := strings.Split(body, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("colour %q: expected 4 components, got %d", s, len(parts))
	}
	var ch [3]uint8
	for i := range ch {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("colour %q: %w", s, err)
		}
		if n < 0 || n > 255 {
			return 0, 0, 0, 0, fmt.Errorf("colour %q: channel %d out of range", s, n)
		}
		ch[i] = uint8(n)
	}
	alpha, err = strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("colour %q: %w", s, err)
	}
	if alpha < 0 || alpha > 1 {
		return 0, 0, 0, 0, fmt.Errorf("colour %q: alpha %v out of range", s, alpha)
	}
	return ch[0], ch[1], ch[2], alpha, nil
}

func hex(r, g, b uint8) string { return fmt.Sprintf("#%02x%02x%02x", r, g, b) }

// Contrast returns the WCAG contrast ratio between two solid colours, from 1
// (identical) to 21 (black against white).
//
// Small text needs 4.5 to pass AA. The palette has already produced one pair
// that failed it in one theme and passed comfortably in the other — a label
// that inverts with its card and a token that did not — so the ratio is worth
// being able to assert rather than eyeball.
func Contrast(a, b string) (float64, error) {
	la, err := luminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := luminance(b)
	if err != nil {
		return 0, err
	}
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05), nil
}

// luminance is the WCAG relative luminance of an opaque colour.
func luminance(hex string) (float64, error) {
	r, g, b, err := ParseHex(hex)
	if err != nil {
		return 0, err
	}
	channel := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b), nil
}
