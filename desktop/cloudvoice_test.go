package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Облачный голос (#478) — тот же запрос, что у english-hub: /speech-model
// шлюза, модель в ai-model-id, ключ в Authorization, звук base64 в ответе.
func TestCloudSpeech_Synth(t *testing.T) {
	type seen struct {
		path, model, auth string
		body              map[string]any
	}
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = append(got, seen{r.URL.Path, r.Header.Get("ai-model-id"), r.Header.Get("Authorization"), b})
		_ = json.NewEncoder(w).Encode(map[string]string{"audio": base64.StdEncoding.EncodeToString([]byte("RIFFwav"))})
	}))
	defer srv.Close()
	c := cloudSpeech{base: srv.URL, client: srv.Client(), key: func() (string, error) { return "vck_test", nil }}

	audio, err := c.synth("gemini:Kore", "Hello there", "en-GB")
	if err != nil || string(audio) != "RIFFwav" {
		t.Fatalf("gemini: %q %v", audio, err)
	}
	if _, err := c.synth("openai:echo", "Привет", "en-GB"); err != nil {
		t.Fatal(err)
	}
	g, o := got[0], got[1]
	if g.path != "/speech-model" || g.model != "google/gemini-3.8-flash-tts" || g.auth != "Bearer vck_test" ||
		g.body["voice"] != "Kore" || g.body["text"] != "Hello there" || g.body["language"] != "en-GB" {
		t.Fatalf("gemini запрос = %+v", g)
	}
	if o.model != "openai/tts-1-hd" || o.body["voice"] != "echo" || o.body["outputFormat"] != "mp3" || o.body["language"] != nil {
		t.Fatalf("openai запрос = %+v", o)
	}
}

// Чужой голос в шлюз не уходит; отказ шлюза назван кодом, без ключа.
func TestCloudSpeech_Refusals(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":"no credits"}`)
	}))
	defer srv.Close()
	c := cloudSpeech{base: srv.URL, client: srv.Client(), key: func() (string, error) { return "vck_secret", nil }}
	for _, v := range []string{"gemini:Nobody", "openai:../x", "Milena"} {
		if _, err := c.synth(v, "текст", ""); err == nil {
			t.Fatalf("%q принят", v)
		}
	}
	if calls != 0 {
		t.Fatalf("чужой голос дошел до шлюза: %d", calls)
	}
	_, err := c.synth("gemini:Kore", "текст", "")
	if err == nil || !strings.Contains(err.Error(), "402") || strings.Contains(err.Error(), "vck_secret") {
		t.Fatalf("отказ = %v", err)
	}
}

// Акцент — только для английского текста: русский ответ облако читает
// по-русски, а не с британским акцентом.
func TestSpeechLang(t *testing.T) {
	for _, c := range []struct{ text, accent, want string }{
		{"Hello, how are you?", "en-GB", "en-GB"},
		{"Hello, how are you?", "", ""},
		{"Привет, как дела? Ok", "en-US", ""},
		{"Run `go test` and check", "en-US", "en-US"},
	} {
		if got := speechLang(c.text, c.accent); got != c.want {
			t.Errorf("speechLang(%q, %q) = %q, want %q", c.text, c.accent, got, c.want)
		}
	}
}

// Облачный голос играет файл и убирает его; Stop во время синтеза
// отбрасывает запоздавший звук — он не играет поверх нового.
func TestSpeaker_Cloud(t *testing.T) {
	played := make(chan string, 2)
	done := make(chan struct{}, 2)
	slow := make(chan struct{})
	s := &speaker{
		done: func() { done <- struct{}{} },
		synth: func(voice, text string) ([]byte, error) {
			if strings.Contains(text, "медленно") {
				<-slow
			}
			return []byte("RIFF"), nil
		},
		player: func(path string) *exec.Cmd {
			played <- path
			return exec.Command("/bin/sh", "-c", "test -s \""+path+"\"")
		},
	}
	if err := s.Speak("gemini:Kore", "Ответ"); err != nil {
		t.Fatal(err)
	}
	path := <-played
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("конец не сообщен")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("файл звука остался: %v", err)
	}

	errc := make(chan error, 1)
	go func() { errc <- s.Speak("gemini:Kore", "медленно") }()
	time.Sleep(100 * time.Millisecond)
	s.Stop()
	close(slow)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-played:
		t.Fatalf("после Stop сыграно %q", p)
	case <-time.After(300 * time.Millisecond):
	}
}
