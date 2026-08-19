// Package embedhttp asks an outside service for the vector of a text.
//
// Внешняя служба, а не библиотека внутри движка — решение осознанное. У движка
// четыре прямые зависимости и образ 13,5 МБ; модель внутри увеличила бы и то,
// и другое на порядок ради одного запроса из двенадцати (замер по набору
// приёмки #232). Здесь только stdlib: HTTP и JSON.
//
// Протокол — тот, что говорит ollama: POST /api/embeddings с {model, prompt} и
// ответ {embedding: [...]}. Выбран не из симпатии, а потому что его повторяют
// несколько локальных служб и он тривиален.
package embedhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Client asks a local embedding service for vectors.
type Client struct {
	URL   string
	Model string
	HTTP  *http.Client
}

// New builds a client with a timeout that fails loudly rather than hanging.
//
// Таймаут короткий намеренно: поиск — интерактивная операция, и человек,
// ждущий полминуты, решит, что движок завис, а не что модель не отвечает.
func New(url, model string) Client {
	return Client{URL: url, Model: model, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Embed returns the vector of the text.
func (c Client) Embed(text string) (search.Vector, error) {
	body, err := json.Marshal(map[string]string{"model": c.Model, "prompt": text})
	if err != nil {
		return nil, fmt.Errorf("embedhttp: не собрать запрос: %w", err)
	}
	resp, err := c.HTTP.Post(c.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedhttp: %s не ответил: %w", c.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedhttp: %s ответил %s", c.URL, resp.Status)
	}
	var out struct {
		Embedding search.Vector `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embedhttp: ответ не разобрать: %w", err)
	}
	if len(out.Embedding) == 0 {
		// Пустой вектор — не «пустой ответ», а поломка: молча вернув его, мы
		// отдали бы поиску вектор, который не похож ни на что, и он честно
		// ничего не найдёт. Человек прочитает это как «в базе такого нет».
		return nil, fmt.Errorf("embedhttp: %s вернул пустой вектор для модели %q", c.URL, c.Model)
	}
	return out.Embedding, nil
}
