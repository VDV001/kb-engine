package desktopconfig_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Модель изображений (#678): адрес — https или этот Мак, имя без управляющих
// символов; пусто целиком — модель не подключена, и это норма.
func TestImagesCheck(t *testing.T) {
	cases := []struct {
		name string
		i    desktopconfig.Images
		ok   bool
		on   bool
	}{
		{"off", desktopconfig.Images{}, true, false},
		{"gateway", desktopconfig.Images{URL: "https://ai-gateway.vercel.sh/v1", Model: "google/gemini-3.1-flash-image"}, true, true},
		{"loopback", desktopconfig.Images{URL: "http://127.0.0.1:8000/v1", Model: "local"}, true, true},
		{"plain http elsewhere", desktopconfig.Images{URL: "http://evil.example/v1", Model: "m"}, false, false},
		{"url without model", desktopconfig.Images{URL: "https://ai-gateway.vercel.sh/v1"}, false, false},
		{"model without url", desktopconfig.Images{Model: "m"}, false, false},
		{"control char", desktopconfig.Images{URL: "https://ai-gateway.vercel.sh/v1", Model: "a\nb"}, false, false},
		{"long model", desktopconfig.Images{URL: "https://ai-gateway.vercel.sh/v1", Model: string(make([]rune, 201))}, false, false},
	}
	for _, c := range cases {
		err := c.i.Check()
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, desktopconfig.ErrChatSettings)) {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.ok && c.i.On() != c.on { // негодные настройки не сохраняются: «подключена» спрашивается только у годных

			t.Errorf("%s: On %v", c.name, c.i.On())
		}
	}
}
