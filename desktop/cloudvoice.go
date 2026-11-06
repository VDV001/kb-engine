package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// Облачные голоса (#478, решение владельца 06.11): Gemini Flash TTS и OpenAI
// tts-1-hd через Vercel AI Gateway — тот же путь, что у english-hub
// (протокол /v4/ai/speech-model, звук base64 в ответе). Замер 06.11: Gemini —
// WAV за 2,9 с, OpenAI — MP3 за 4,4 с на одной фразе.

// gatewayAudio — адрес аудио-протокола шлюза.
const gatewayAudio = "https://ai-gateway.vercel.sh/v4/ai"

// cloudVoice — голос облака для настроек: id «gemini:Kore», подпись и
// характер; Pricey — дороже (OpenAI, около 0,8 цента за реплику против 0,2).
type cloudVoice struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Tag    string `json:"tag"`
	Pricey bool   `json:"pricey,omitempty"`
}

// cloudVoices — описания голосов Gemini из документации Google, как в english-hub.
var cloudVoices = []cloudVoice{
	{"gemini:Kore", "Kore", "уверенный", false}, {"gemini:Sulafat", "Sulafat", "теплый", false},
	{"gemini:Aoede", "Aoede", "легкий", false}, {"gemini:Zephyr", "Zephyr", "яркий", false},
	{"gemini:Despina", "Despina", "плавный", false}, {"gemini:Puck", "Puck", "бодрый", false},
	{"gemini:Charon", "Charon", "размеренный", false}, {"gemini:Orus", "Orus", "твердый", false},
	{"gemini:Achird", "Achird", "дружелюбный", false}, {"gemini:Alnilam", "Alnilam", "собранный", false},
	{"gemini:Sadaltager", "Sadaltager", "знающий", false},
	{"openai:nova", "Nova", "", true}, {"openai:shimmer", "Shimmer", "", true},
	{"openai:echo", "Echo", "", true}, {"openai:onyx", "Onyx", "", true},
}

var ttsModel = map[string]string{"gemini": "google/gemini-3.8-flash-tts", "openai": "openai/tts-1-hd"}

// isCloudVoice reports whether voice is read by the cloud, а не say.
func isCloudVoice(voice string) bool {
	return strings.HasPrefix(voice, "gemini:") || strings.HasPrefix(voice, "openai:")
}

func knownCloudVoice(voice string) bool {
	for _, v := range cloudVoices {
		if v.ID == voice {
			return true
		}
	}
	return false
}

// cloudSpeech synthesizes speech at the gateway; key — ключ шлюза.
type cloudSpeech struct {
	base   string
	client *http.Client
	key    func() (string, error)
}

// synth returns the audio of text read by voice; lang — en-US/en-GB для
// английского текста (Gemini), пусто — язык определит модель.
func (c cloudSpeech) synth(voice, text, lang string) ([]byte, error) {
	if !knownCloudVoice(voice) {
		return nil, fmt.Errorf("облачного голоса %q нет", voice)
	}
	key, err := c.key()
	if err != nil {
		return nil, err
	}
	req, err := c.request(voice, text, lang, key)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("облачный голос: шлюз не ответил (%T)", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("облачный голос: шлюз ответил %d", resp.StatusCode)
	}
	var out struct {
		Audio string `json:"audio"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("облачный голос: ответ шлюза не прочитан: %w", err)
	}
	audio, err := base64.StdEncoding.DecodeString(out.Audio)
	if err != nil || len(audio) == 0 {
		return nil, fmt.Errorf("облачный голос: шлюз не прислал звук")
	}
	return audio, nil
}

// request builds the /speech-model call: модель в ai-model-id, у Gemini —
// язык, у OpenAI — mp3.
func (c cloudSpeech) request(voice, text, lang, key string) (*http.Request, error) {
	kind, name, _ := strings.Cut(voice, ":")
	body := map[string]string{"text": text, "voice": name}
	if kind == "gemini" && lang != "" {
		body["language"] = lang
	}
	if kind == "openai" {
		body["outputFormat"] = "mp3"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/speech-model", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("ai-gateway-protocol-version", "0.0.1")
	req.Header.Set("ai-model-id", ttsModel[kind])
	return req, nil
}

// cloudListen — облачное распознавание (#478): значение «Слушает» → модель
// шлюза. mini — быстрее и вдвое дешевле, best — точнее на шуме.
var cloudListen = map[string]string{
	"cloud:mini": "openai/gpt-4o-mini-transcribe",
	"cloud:best": "openai/gpt-4o-transcribe",
}

// transcribe returns the Russian text of wav via /transcription-model шлюза.
func (c cloudSpeech) transcribe(ctx context.Context, wav []byte, model string) (string, error) {
	key, err := c.key()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(map[string]any{
		"audio": base64.StdEncoding.EncodeToString(wav), "mediaType": "audio/wav",
		"providerOptions": map[string]any{"openai": map[string]string{"language": "ru"}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/transcription-model", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("ai-gateway-protocol-version", "0.0.1")
	req.Header.Set("ai-model-id", model)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("облачное распознавание: шлюз не ответил (%T)", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("облачное распознавание: шлюз ответил %d", resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("облачное распознавание: ответ не прочитан: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// speechLang returns accent for English text, пусто для русского и смешанного:
// русский ответ с британским акцентом звучит хуже, чем без подсказки.
// ponytail: язык — по большинству букв; потолок — фраза из английских терминов
// в русском ответе читается русским голосом. Путь: делить текст на куски по языку.
func speechLang(text, accent string) string {
	if accent == "" {
		return ""
	}
	var lat, cyr int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Latin, r):
			lat++
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		}
	}
	if cyr > 0 || lat == 0 {
		return ""
	}
	return accent
}
