//go:build !windows

package main

import (
	"strings"
	"testing"
)

// Голос Мака — системный say с именем голоса отдельным аргументом.
func TestSayCommand(t *testing.T) {
	if c := sayCommand("Milena"); strings.Join(c.Args, " ") != "/usr/bin/say -v Milena" {
		t.Fatalf("say = %q", c.Args)
	}
}

// «Добавить голоса в macOS…» открывает раздел голосов Настроек — адрес
// зашит, из окна ничего не приходит.
func TestVoiceSettingsCommand(t *testing.T) {
	c := voiceSettingsCommand()
	if strings.Join(c.Args, " ") != "/usr/bin/open "+voiceSettingsURL || !strings.HasPrefix(voiceSettingsURL, "x-apple.systempreferences:") {
		t.Fatalf("команда = %q", c.Args)
	}
}
