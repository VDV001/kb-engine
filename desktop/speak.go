package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Голос ответа (#436): встроенная в macOS команда say читает ответ выбранным
// голосом. Текст идет во вход, а не в аргументы: он не виден в списке
// процессов и не упирается в длину командной строки.

var (
	mdEmph   = regexp.MustCompile("\\*\\*|__|`")
	mdCite   = regexp.MustCompile(`\s*(\[\d+\])+`)
	mdLink   = regexp.MustCompile(`https?://\S+`)
	mdBullet = regexp.MustCompile(`(?m)^\s*[-*•]\s+`)
)

// speakable strips what a voice should not pronounce: разметку, ссылки [n] и адреса.
func speakable(text string) string {
	text = mdEmph.ReplaceAllString(text, "")
	text = mdCite.ReplaceAllString(text, "")
	text = mdLink.ReplaceAllString(text, "ссылку")
	text = mdBullet.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// speaker reads one answer at a time; новый ответ прерывает прежний.
type speaker struct {
	// command builds the reader for a voice; nil — /usr/bin/say.
	command func(voice string) *exec.Cmd
	// synth returns the audio of a cloud voice (#478); nil — облака нет.
	synth func(voice, text string) ([]byte, error)
	// player plays an audio file; nil — /usr/bin/afplay.
	player func(path string) *exec.Cmd
	done   func() // чтение кончилось или прервано

	mu  sync.Mutex
	cmd *exec.Cmd
	gen int // растет на каждом Speak и Stop: запоздавший облачный звук не играет
}

// Speak reads text with voice ("" — голос системы, «gemini:…»/«openai:…» —
// облако); пустое не читается.
func (s *speaker) Speak(voice, text string) error {
	text = speakable(text)
	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.killLocked() // новый ответ прерывает прежний; конец сообщит только новый
	if text == "" {
		s.mu.Unlock()
		// Читать нечего — конец сразу: разговор голосом иначе ждал бы вечно.
		if s.done != nil {
			go s.done()
		}
		return nil
	}
	if !isCloudVoice(voice) {
		defer s.mu.Unlock()
		build := s.command
		if build == nil {
			build = sayCommand
		}
		cmd := build(voice)
		cmd.Stdin = strings.NewReader(text)
		return s.startLocked(cmd, "")
	}
	s.mu.Unlock()
	if s.synth == nil {
		return errors.New("облачный голос работает только в окне приложения")
	}
	// Сеть — без замка: Stop успевает прервать, пока облако синтезирует.
	audio, err := s.synth(voice, text)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "dvk-voice-*.audio")
	if err != nil {
		return err
	}
	path := f.Name()
	_, werr := f.Write(audio)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return werr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen { // пока синтезировали, ответ прерван или сменился новым
		_ = os.Remove(path)
		return nil
	}
	play := s.player
	if play == nil {
		play = afplayCommand
	}
	return s.startLocked(play(path), path)
}

// startLocked starts cmd as the current reading; file — звук облака, убрать после.
func (s *speaker) startLocked(cmd *exec.Cmd, file string) error {
	if err := cmd.Start(); err != nil {
		if file != "" {
			_ = os.Remove(file)
		}
		return err
	}
	s.cmd = cmd
	go func() {
		_ = cmd.Wait() // прерванное чтение — не ошибка
		if file != "" {
			_ = os.Remove(file)
		}
		s.mu.Lock()
		own := s.cmd == cmd
		if own {
			s.cmd = nil
		}
		s.mu.Unlock()
		// Прерванное новым ответом — не конец: иначе микрофон включился бы
		// поверх нового чтения и бот слушал бы сам себя (ревью #465).
		if own && s.done != nil {
			s.done()
		}
	}()
	return nil
}

func (s *speaker) killLocked() {
	if s.cmd != nil {
		_ = s.cmd.Process.Kill()
		s.cmd = nil
	}
}

func afplayCommand(path string) *exec.Cmd { return exec.Command("/usr/bin/afplay", path) }

func sayCommand(voice string) *exec.Cmd {
	if voice == "" {
		return exec.Command("/usr/bin/say")
	}
	return exec.Command("/usr/bin/say", "-v", voice)
}

// Stop interrupts the reading, и облачный звук, который еще синтезируется.
func (s *speaker) Stop() {
	s.mu.Lock()
	s.gen++
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil {
		_ = cmd.Process.Kill() // свое чтение: горутина Wait сообщит конец
	}
}

var voiceLine = regexp.MustCompile(`^(.+?)\s+([a-z]{2}_[A-Z]{2})\s+#`)

// parseVoices returns the voices of `say -v ?` whose locale starts with prefix.
func parseVoices(out, prefix string) []string {
	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		if m := voiceLine.FindStringSubmatch(line); m != nil && strings.HasPrefix(m[2], prefix) {
			names = append(names, strings.TrimSpace(m[1]))
		}
	}
	return names
}

// englishVoices — голоса Mac для акцента, лучшие первыми; звуковые эффекты
// из списка say (Bells, Bad News) голосом не считаются.
var englishVoices = map[string][]string{
	"en-US": {"Samantha", "Ava", "Allison", "Alex", "Tom", "Susan"},
	"en-GB": {"Daniel", "Kate", "Serena", "Oliver", "Stephanie"},
}

// englishVoiceIn picks the voice for accent from `say -v ?` output; пусто — нет такого.
func englishVoiceIn(out, accent string) string {
	have := parseVoices(out, strings.Replace(accent, "-", "_", 1))
	for _, want := range englishVoices[accent] {
		for _, v := range have {
			if v == want || strings.HasPrefix(v, want+" (") {
				return v
			}
		}
	}
	return ""
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

// previewSample — пробная фраза голоса в настройках (#478).
const previewSample = "Здравствуйте! Так я буду читать ответы."

// previewVoice says the sample with voice: «системный» ("") или голос из
// списка Мака — чужая строка из окна в say не уходит.
func previewVoice(s *speaker, voices func() []string, voice string) error {
	if voice != "" && !knownCloudVoice(voice) && !slices.Contains(voices(), voice) {
		return fmt.Errorf("голоса %q нет на этом Маке", voice)
	}
	return s.Speak(voice, previewSample)
}

// voiceSettingsURL — раздел «Устной контент» Настроек macOS, где скачивают
// голоса Premium и Enhanced.
const voiceSettingsURL = "x-apple.systempreferences:com.apple.preference.universalaccess?SpokenContent"

func voiceSettingsCommand() *exec.Cmd { return exec.Command("/usr/bin/open", voiceSettingsURL) }
