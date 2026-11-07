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

	"github.com/daniil/kb-engine/internal/domain"
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

// Облачное распознавание (#478) — /transcription-model шлюза (OpenAI-
// совместимого /audio/transcriptions у шлюза нет, замер 06.11 — 404): WAV
// в base64, язык русский, модель в ai-model-id.
func TestCloudSpeech_Transcribe(t *testing.T) {
	var model string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcription-model" {
			http.NotFound(w, r)
			return
		}
		model = r.Header.Get("ai-model-id")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"text":" Как настроить голос? ","segments":[]}`)
	}))
	defer srv.Close()
	c := cloudSpeech{base: srv.URL, client: srv.Client(), key: func() (string, error) { return "vck_test", nil }}
	text, err := c.transcribe(t.Context(), []byte("RIFFwav"), cloudListen["cloud:best"])
	if err != nil || text != "Как настроить голос?" {
		t.Fatalf("text = %q, %v", text, err)
	}
	opts, _ := body["providerOptions"].(map[string]any)
	oa, _ := opts["openai"].(map[string]any)
	if model != "openai/gpt-4o-transcribe" || body["mediaType"] != "audio/wav" || body["audio"] != base64.StdEncoding.EncodeToString([]byte("RIFFwav")) || oa["language"] != "ru" {
		t.Fatalf("model %q body %v", model, body)
	}
	if cloudListen["cloud:mini"] != "openai/gpt-4o-mini-transcribe" {
		t.Fatalf("mini = %q", cloudListen["cloud:mini"])
	}
}

// Облачные распознавание и голос пишутся в журнал вызовов (#493): назначение,
// модель шлюза, время, ошибка. Счета токенов от шлюза не ждем — вызов «без
// счета», а не ноль.
func TestCloudSpeech_recordsCalls(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/speech-model" {
			_, _ = io.WriteString(w, `{"audio":"`+base64.StdEncoding.EncodeToString([]byte("ID3"))+`"}`)
			return
		}
		_, _ = io.WriteString(w, `{"text":"привет"}`)
	}))
	defer srv.Close()
	rec := &callsRec{}
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := cloudSpeech{base: srv.URL, client: srv.Client(), key: func() (string, error) { return "vck_test", nil }, calls: rec, now: func() time.Time { return at }}
	if _, err := c.transcribe(t.Context(), []byte("RIFFwav"), cloudListen["cloud:mini"]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.synth("gemini:Kore", "Привет", ""); err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := c.transcribe(t.Context(), []byte("RIFFwav"), cloudListen["cloud:mini"]); err == nil {
		t.Fatal("ошибка шлюза не дошла")
	}
	if len(rec.calls) != 3 {
		t.Fatalf("записано %d вызовов", len(rec.calls))
	}
	l, s, f := rec.calls[0], rec.calls[1], rec.calls[2]
	if l.Purpose() != domain.CallListen || l.Model() != "openai/gpt-4o-mini-transcribe" || l.Counted() || l.Failed() || !l.At().Equal(at) {
		t.Fatalf("распознавание = %+v", l)
	}
	if s.Purpose() != domain.CallSpeak || s.Model() != ttsModel["gemini"] || s.Failed() {
		t.Fatalf("голос = %+v", s)
	}
	if !f.Failed() || f.Purpose() != domain.CallListen {
		t.Fatalf("отказ = %+v", f)
	}
}
