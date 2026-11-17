// Package imagegen is the client of an OpenAI-compatible gateway that draws
// pictures (#678): модель изображений человек подключает сам — адрес, ключ и
// имя, баланс пополняет сам.
package imagegen

import (
	"context"
	"errors"
	"net/http"
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
}

// Generate draws a picture for the prompt; refs — референсы, уходят data URL.
func (c Client) Generate(ctx context.Context, prompt string, refs [][]byte) (Image, error) {
	return Image{}, nil
}
