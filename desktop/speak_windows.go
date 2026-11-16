package main

import (
	"os"
	"os/exec"
	"syscall"
)

// Голоса Windows (#622, этап 5): System.Speech из системного PowerShell —
// голоса, установленные в Windows (русский — Microsoft Irina). Текст идет в
// stdin, голос и путь — переменными окружения: из окна в текст скрипта ничего
// не попадает. Облачный звук играет MediaPlayer (WPF).

// speakScript reads stdin в UTF-8 (без этого кириллица придет кодовой
// страницей консоли) и говорит голосом KB_VOICE или голосом по умолчанию.
const speakScript = `[Console]::InputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Speech
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
if ($env:KB_VOICE) { $s.SelectVoice($env:KB_VOICE) }
$s.Speak([Console]::In.ReadToEnd())`

// playScript plays KB_FILE до конца: MediaPlayer асинхронный, поэтому ждем
// длительность, а прерывание — это убить процесс, как afplay.
const playScript = `Add-Type -AssemblyName PresentationCore
$p = New-Object System.Windows.Media.MediaPlayer
$p.Open([Uri]$env:KB_FILE)
for ($i = 0; -not $p.NaturalDuration.HasTimeSpan -and $i -lt 100; $i++) { Start-Sleep -Milliseconds 50 }
$p.Play()
Start-Sleep -Milliseconds ([int]$p.NaturalDuration.TimeSpan.TotalMilliseconds + 300)`

const voicesScript = `Add-Type -AssemblyName System.Speech
[Console]::OutputEncoding = [Text.Encoding]::UTF8
(New-Object System.Speech.Synthesis.SpeechSynthesizer).GetInstalledVoices() | Where-Object Enabled | ForEach-Object { $_.VoiceInfo.Name + "` + "`t" + `" + $_.VoiceInfo.Culture.Name }`

func psCommand(script string, env ...string) *exec.Cmd {
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

func sayCommand(voice string) *exec.Cmd { return psCommand(speakScript, "KB_VOICE="+voice) }

func afplayCommand(path string) *exec.Cmd { return psCommand(playScript, "KB_FILE="+path) }

func installedVoices(prefix string) []string {
	out, err := psCommand(voicesScript).Output()
	if err != nil {
		return nil
	}
	return windowsVoices(string(out), prefix)
}

// englishVoice — первый голос акцента: en-US (Zira, David) или en-GB (Hazel).
func englishVoice(accent string) string {
	if v := installedVoices(accent); len(v) > 0 {
		return v[0]
	}
	return ""
}

func russianVoices() []string { return installedVoices("ru-") }

// voiceSettingsURL — раздел «Время и язык → Речь» Параметров Windows: там
// ставят голоса.
const voiceSettingsURL = "ms-settings:speech"

func voiceSettingsCommand() *exec.Cmd {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", voiceSettingsURL)
}
