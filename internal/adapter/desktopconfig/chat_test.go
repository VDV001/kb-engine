package desktopconfig_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Голос ответа — имя голоса Mac из настроек: короткое и без управляющих
// символов; пусто — голос системы.
func TestChatCheck_Voice(t *testing.T) {
	base := desktopconfig.Chat{URL: desktopconfig.DefaultChatURL, Model: "m"}
	for _, v := range []string{"", "Milena", "Yuri (Enhanced)"} {
		c := base
		c.Voice = v
		if err := c.Check(); err != nil {
			t.Errorf("голос %q отвергнут: %v", v, err)
		}
	}
	for _, v := range []string{"Mil\nena", strings.Repeat("я", 65), "-o /tmp/x"} {
		c := base
		c.Voice = v
		if err := c.Check(); !errors.Is(err, desktopconfig.ErrChatSettings) {
			t.Errorf("голос %q принят: %v", v, err)
		}
	}
}

// «Слушает» — id подключения распознавания или пусто (Apple Speech);
// подключение должно существовать — проверяет окно, здесь только форма.
func TestChatCheck_Listen(t *testing.T) {
	c := desktopconfig.Chat{URL: desktopconfig.DefaultChatURL, Model: "m", Listen: "s1"}
	if err := c.Check(); err != nil {
		t.Fatalf("отвергнуто: %v", err)
	}
	c.Listen = "s1\n"
	if err := c.Check(); !errors.Is(err, desktopconfig.ErrChatSettings) {
		t.Fatalf("принято: %v", err)
	}
}

// Режим микрофона (#478): пусто — нажал-нажал, hold — удерживать, auto —
// слушает сам; другое не сохраняется.
func TestChatCheck_Mic(t *testing.T) {
	base := desktopconfig.Chat{URL: desktopconfig.DefaultChatURL, Model: "m"}
	for _, m := range []string{desktopconfig.MicToggle, desktopconfig.MicHold, desktopconfig.MicAuto} {
		c := base
		c.Mic = m
		if err := c.Check(); err != nil {
			t.Errorf("режим %q отвергнут: %v", m, err)
		}
	}
	c := base
	c.Mic = "push"
	if err := c.Check(); !errors.Is(err, desktopconfig.ErrChatSettings) {
		t.Fatalf("режим push принят: %v", err)
	}
}

// Акцент голоса (#478): пусто — по языку ответа, en-US, en-GB.
func TestChatCheck_Accent(t *testing.T) {
	base := desktopconfig.Chat{URL: desktopconfig.DefaultChatURL, Model: "m"}
	for _, a := range []string{"", "en-US", "en-GB"} {
		c := base
		c.Accent = a
		if err := c.Check(); err != nil {
			t.Errorf("акцент %q отвергнут: %v", a, err)
		}
	}
	c := base
	c.Accent = "fr-FR"
	if err := c.Check(); !errors.Is(err, desktopconfig.ErrChatSettings) {
		t.Fatalf("fr-FR принят: %v", err)
	}
}
