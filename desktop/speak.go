package main

import (
	"os/exec"
	"regexp"
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
	done    func() // чтение кончилось или прервано

	mu  sync.Mutex
	cmd *exec.Cmd
}

// Speak reads text with voice ("" — голос системы); пустое не читается.
func (s *speaker) Speak(voice, text string) error {
	text = speakable(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		_ = s.cmd.Process.Kill() // новый ответ прерывает прежний; конец сообщит только новый
		s.cmd = nil
	}
	if text == "" {
		// Читать нечего — конец сразу: разговор голосом иначе ждал бы вечно.
		if s.done != nil {
			go s.done()
		}
		return nil
	}
	build := s.command
	if build == nil {
		build = sayCommand
	}
	cmd := build(voice)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	go func() {
		_ = cmd.Wait() // прерванное чтение — не ошибка
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

func sayCommand(voice string) *exec.Cmd {
	if voice == "" {
		return exec.Command("/usr/bin/say")
	}
	return exec.Command("/usr/bin/say", "-v", voice)
}

// Stop interrupts the reading.
func (s *speaker) Stop() {
	s.mu.Lock()
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

// russianVoices lists the Russian voices of this Mac.
func russianVoices() []string {
	out, err := exec.Command("/usr/bin/say", "-v", "?").Output()
	if err != nil {
		return nil
	}
	return parseVoices(string(out), "ru_")
}
