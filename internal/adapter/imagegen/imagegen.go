// Package imagegen is the client of an OpenAI-compatible gateway that draws
// pictures (#678): модель изображений человек подключает сам — адрес, ключ и
// имя, баланс пополняет сам.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Отказы клиента; тексты — для человека.
var (
	ErrNoCredit   = errors.New("на балансе шлюза нет средств для изображений: пополните его")
	ErrKeyRefused = errors.New("шлюз не принял ключ или его лимит исчерпан: проверьте ключ и лимит")
	ErrNoImage    = errors.New("модель не вернула картинку")
	ErrTooBig     = errors.New("ответ шлюза больше предела")
)

// Client draws through URL/chat/completions.
type Client struct {
	URL      string // адрес шлюза с /v1
	Model    string
	Key      string
	HTTP     *http.Client
	MaxBytes int64 // предел ответа; 0 — 32 МБ
}

// Image is a generated picture.
type Image struct {
	Data []byte
	MIME string
	Cost string // цена вызова от шлюза, доллары; пусто — шлюз не прислал
}

const defaultMax = 32 << 20

type part struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type reply struct {
	ProviderMetadata struct {
		Gateway struct {
			Cost json.RawMessage `json:"cost"`
		} `json:"gateway"`
	} `json:"providerMetadata"`
	Choices []struct {
		Message struct {
			Images []struct {
				ImageURL imageURL `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
}

// Generate draws a picture for the prompt; refs — референсы, уходят data URL.
func (c Client) Generate(ctx context.Context, prompt string, refs [][]byte) (Image, error) {
	content := []part{{Type: "text", Text: prompt}}
	for _, r := range refs {
		content = append(content, part{Type: "image_url", ImageURL: &imageURL{URL: "data:" + http.DetectContentType(r) + ";base64," + base64.StdEncoding.EncodeToString(r)}})
	}
	body, err := json.Marshal(map[string]any{
		"model":      c.Model,
		"modalities": []string{"text", "image"},
		"messages":   []map[string]any{{"role": "user", "content": content}},
	})
	if err != nil {
		return Image{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Image{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	resp, err := h.Do(req)
	if err != nil {
		return Image{}, errors.New("шлюз не ответил: " + redactKey(err.Error(), c.Key))
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp.StatusCode); err != nil {
		return Image{}, err
	}
	raw, err := readLimited(resp.Body, c.MaxBytes)
	if err != nil {
		return Image{}, err
	}
	var r reply
	if json.Unmarshal(raw, &r) != nil || len(r.Choices) == 0 || len(r.Choices[0].Message.Images) == 0 {
		return Image{}, ErrNoImage
	}
	img, err := parseDataURL(r.Choices[0].Message.Images[0].ImageURL.URL)
	img.Cost = r.cost()
	return img, err
}

// cost returns the gateway price as text — строка или число; иначе пусто:
// цена — учет, и ее странный тип не должен ломать получение картинки.
func (r reply) cost() string {
	raw := r.ProviderMetadata.Gateway.Cost
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

// statusError turns the gateway's code into words for the person.
func statusError(code int) error {
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusPaymentRequired:
		return ErrNoCredit
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrKeyRefused
	}
	return fmt.Errorf("шлюз ответил кодом %d", code)
}

// readLimited reads the reply up to limit bytes (0 — по умолчанию): больше — ErrTooBig.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = defaultMax
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrTooBig
	}
	return raw, nil
}

// parseDataURL reads data:<mime>;base64,<...>: ссылка на чужой адрес не
// скачивается — окно не ходит по адресам из ответа модели.
func parseDataURL(u string) (Image, error) {
	head, b64, ok := strings.Cut(u, ",")
	mime, enc, _ := strings.Cut(strings.TrimPrefix(head, "data:"), ";")
	if !ok || !strings.HasPrefix(head, "data:image/") || enc != "base64" {
		return Image{}, ErrNoImage
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) == 0 {
		return Image{}, ErrNoImage
	}
	return Image{Data: data, MIME: mime}, nil
}

// redactKey keeps the key out of an error text.
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}
