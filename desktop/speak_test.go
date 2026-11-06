package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ответ читается вслух без разметки и ссылок [n]: голос не произносит
// «звездочка-звездочка» и «один в скобках».
func TestSpeakable(t *testing.T) {
	got := speakable("Срок — **30 дней** [1], штраф `0,1%` [2][3].\n\nСм. https://example.com/x")
	if got != "Срок — 30 дней, штраф 0,1%.\n\nСм. ссылку" {
		t.Fatalf("speakable = %q", got)
	}
}

// Голос — команда say с выбранным голосом, текст во входе, а не в
// аргументах; стоп прерывает чтение; конец сообщается один раз.
func TestSpeaker(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "said")
	var voices []string
	done := make(chan struct{}, 2)
	s := &speaker{done: func() { done <- struct{}{} }, command: func(voice string) *exec.Cmd {
		voices = append(voices, voice)
		return exec.Command("/bin/sh", "-c", "cat > \""+out+"\"; sleep 5")
	}}
	if err := s.Speak("Milena", "Срок **30 дней** [1]."); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	s.Stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("конец чтения не сообщен")
	}
	said, _ := os.ReadFile(out)
	if strings.TrimSpace(string(said)) != "Срок 30 дней." || strings.Join(voices, ",") != "Milena" {
		t.Fatalf("сказано %q голосом %q", said, voices)
	}
	if c := sayCommand("Milena"); strings.Join(c.Args, " ") != "/usr/bin/say -v Milena" {
		t.Fatalf("say = %q", c.Args)
	}
	if err := s.Speak("", "  "); err != nil { // пустое не читается и не ошибка
		t.Fatal(err)
	}
}

// Голоса — русские из say -v ?: имя может быть из нескольких слов.
func TestParseVoices(t *testing.T) {
	out := "Alex                en_US    # Most people recognize me by my voice.\nMilena              ru_RU    # Здравствуйте! Меня зовут Милена.\nYuri (Enhanced)     ru_RU    # Здравствуйте!\nAmélie              fr_CA    # Bonjour\n"
	if got := strings.Join(parseVoices(out, "ru_"), ","); got != "Milena,Yuri (Enhanced)" {
		t.Fatalf("voices = %q", got)
	}
}

// Конец чтения сообщается только о своем чтении: прерванное новым ответом не
// будит микрофон поверх нового (петля «бот слушает сам себя»); пустой ответ —
// конец сразу, иначе разговор ждал бы вечно (ревью #465).
func TestSpeaker_DoneOnlyForOwnReading(t *testing.T) {
	done := make(chan struct{}, 4)
	s := &speaker{done: func() { done <- struct{}{} }, command: func(string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "cat >/dev/null; sleep 5")
	}}
	if err := s.Speak("", "первый ответ"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := s.Speak("", "второй ответ"); err != nil { // прерывает первый
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("прерванное чтение сообщило конец")
	case <-time.After(500 * time.Millisecond):
	}
	s.Stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("остановленное чтение не сообщило конец")
	}
	if err := s.Speak("", "[1] **"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("пустой ответ не сообщил конец")
	}
}

// Проба голоса в настройках (#478): говорит пробную фразу выбранным
// голосом; голос — только «системный» или из списка Мака: в say не уходит
// чужая строка из окна.
func TestPreviewVoice(t *testing.T) {
	var got []string
	s := &speaker{command: func(voice string) *exec.Cmd {
		got = append(got, voice)
		return exec.Command("/usr/bin/true")
	}}
	list := func() []string { return []string{"Milena", "Yuri (Enhanced)"} }
	for _, v := range []string{"", "Milena", "Yuri (Enhanced)"} {
		if err := previewVoice(s, list, v); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
	}
	if strings.Join(got, ",") != ",Milena,Yuri (Enhanced)" {
		t.Fatalf("голоса = %q", got)
	}
	for _, v := range []string{"Alex", "-o /tmp/x", "Milena\n"} {
		if err := previewVoice(s, list, v); err == nil {
			t.Fatalf("%q: чужой голос принят", v)
		}
	}
	if len(got) != 3 {
		t.Fatalf("чужой голос дошел до say: %q", got)
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
