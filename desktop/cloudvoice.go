package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
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
	// calls keeps every cloud speech call for the statistics (#493); nil — не считать.
	calls usage.Recorder
	now   func() time.Time
}

// gatewayBill is the price of a call the gateway sends with the answer (замер
// 07.11 на живых /transcription-model и /speech-model): доллары строкой,
// токенов в ответе нет. Сырым JSON: цена — учет, и другой ее тип (число,
// null) не должен ломать разбор текста и звука (ревью #507).
type gatewayBill struct {
	ProviderMetadata struct {
		Gateway struct {
			Cost json.RawMessage `json:"cost"`
		} `json:"gateway"`
	} `json:"providerMetadata"`
}

// cost returns the gateway price as text: строка или число; иначе пусто.
func (b gatewayBill) cost() string {
	raw := b.ProviderMetadata.Gateway.Cost
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// record writes one cloud speech call to the journal; журнал не мешает речи —
// его отказ молча пропускается, как у счетчика модели. cost — цена от шлюза;
// пустая или кривая — вызов «без счета», а не бесплатный.
func (c cloudSpeech) record(p domain.CallPurpose, model string, start time.Time, failed bool, cost string) {
	if c.calls == nil {
		return
	}
	took := max(c.clock()().Sub(start), 0)
	var call domain.ModelCall
	var err error
	if pico, perr := domain.ParseDollars(cost); perr == nil {
		call, err = domain.NewBilledModelCall(start, p, model, pico, took, failed)
	} else {
		call, err = domain.NewModelCall(start, p, model, 0, 0, false, took, took, failed)
	}
	if err == nil {
		_ = c.calls.Record(call)
	}
}

func (c cloudSpeech) clock() func() time.Time {
	if c.now == nil {
		return time.Now
	}
	return c.now
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
	kind, _, _ := strings.Cut(voice, ":")
	start, failed, cost := c.clock()(), true, ""
	defer func() { c.record(domain.CallSpeak, ttsModel[kind], start, failed, cost) }()
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
		gatewayBill
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("облачный голос: ответ шлюза не прочитан: %w", err)
	}
	cost = out.cost()
	audio, err := base64.StdEncoding.DecodeString(out.Audio)
	if err != nil || len(audio) == 0 {
		return nil, fmt.Errorf("облачный голос: шлюз не прислал звук")
	}
	failed = false
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
	start, failed, cost := c.clock()(), true, ""
	defer func() { c.record(domain.CallListen, model, start, failed, cost) }()
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
		gatewayBill
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("облачное распознавание: ответ не прочитан: %w", err)
	}
	cost = out.cost()
	failed = false
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
