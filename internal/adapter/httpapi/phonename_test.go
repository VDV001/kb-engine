package httpapi

import "testing"

// Имя телефона в окне — по его браузеру, а не «p-9a7ffa8f» (просьба владельца 16.11).
func TestPhoneName(t *testing.T) {
	for _, c := range []struct{ ua, model, want string }{
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", "", "iPhone"},
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15", "", "iPad"},
		{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36", `"SM-S918B"`, "Samsung SM-S918B"},
		{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 Chrome/131.0.0.0 Mobile Safari/537.36", `"Pixel 8"`, "Pixel 8"},
		{"Mozilla/5.0 (Linux; Android 14; SM-A546B) AppleWebKit/537.36 Chrome/131.0.0.0 Mobile Safari/537.36", "", "Samsung SM-A546B"},
		{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 Chrome/131.0.0.0 Mobile Safari/537.36", "", "Android"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15", "", "Mac"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36", "", "Windows"},
		{"", "", ""},
		{"x", `"` + string(make([]byte, 200)) + `"`, ""},
		{"Mozilla/5.0 (Linux; Android 10; K) Chrome/131", `"<script>"`, "Android"},
	} {
		if got := phoneName(c.ua, c.model); got != c.want {
			t.Errorf("phoneName(%.40q, %q) = %q, want %q", c.ua, c.model, got, c.want)
		}
	}
}
