//go:build !windows

package main

import "os/exec"

// Голоса Мака: say и afplay. Под Windows — speak_windows.go.

func afplayCommand(path string) *exec.Cmd { return exec.Command("/usr/bin/afplay", path) }

func sayCommand(voice string) *exec.Cmd {
	if voice == "" {
		return exec.Command("/usr/bin/say")
	}
	return exec.Command("/usr/bin/say", "-v", voice)
}

func englishVoice(accent string) string {
	out, err := exec.Command("/usr/bin/say", "-v", "?").Output()
	if err != nil {
		return ""
	}
	return englishVoiceIn(string(out), accent)
}

// russianVoices lists the Russian voices of this Mac.
func russianVoices() []string {
	out, err := exec.Command("/usr/bin/say", "-v", "?").Output()
	if err != nil {
		return nil
	}
	return parseVoices(string(out), "ru_")
}

// voiceSettingsURL — раздел «Устной контент» Настроек macOS, где скачивают
// голоса Premium и Enhanced.
const voiceSettingsURL = "x-apple.systempreferences:com.apple.preference.universalaccess?SpokenContent"

func voiceSettingsCommand() *exec.Cmd { return exec.Command("/usr/bin/open", voiceSettingsURL) }
