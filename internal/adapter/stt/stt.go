// Package stt is a client of the OpenAI-compatible /audio/transcriptions API:
// так отвечают whisper.cpp server, faster-whisper-server и OpenAI. Окно DVK
// отдает ему запись диктовки, когда в «Слушает» выбран не Apple Speech. Без
// SDK: один multipart-запрос.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// Отказы: окно называет их словами человека.
var (
	ErrUnauthorized = errors.New("speech server refused the key")
	ErrNotFound     = errors.New("no speech recognition at this address")
	ErrServer       = errors.New("speech server error")
	ErrInsecureURL  = errors.New("key only goes over https or to this Mac")
)

// Client sends recordings to one server at BaseURL (…/v1). Key пусто — без
// Authorization (whisper.cpp на этом Маке).
type Client struct {
	BaseURL, Key, Model string
	HTTP                *http.Client
}

// Transcribe returns the text of a WAV recording in language lang.
func (c Client) Transcribe(ctx context.Context, wav []byte, lang string) (string, error) {
	if c.Key != "" && !safeForKey(c.BaseURL) {
		return "", ErrInsecureURL
	}
	body, ctype, err := c.form(wav, lang)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/audio/transcriptions", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", ctype)
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrServer, err)
	}
	defer func() { _ = res.Body.Close() }()
	if err := statusErr(res.StatusCode); err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: %w", ErrServer, err)
	}
	return strings.TrimSpace(out.Text), nil
}

// form builds the multipart body: запись, модель, язык.
func (c Client) form(wav []byte, lang string) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "speech.wav")
	if err != nil {
		return nil, "", err
	}
	_, _ = part.Write(wav)
	model := c.Model
	if model == "" {
		model = "whisper-1"
	}
	_ = w.WriteField("model", model)
	_ = w.WriteField("language", lang)
	_ = w.WriteField("response_format", "json")
	return &body, w.FormDataContentType(), w.Close()
}

// statusErr names a refusal; текст ответа не берется — в нем бывает эхо ключа.
func statusErr(code int) error {
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	return fmt.Errorf("%w: status %d", ErrServer, code)
}

// safeForKey reports whether a key may go to base: https или этот Мак.
func safeForKey(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && loopback.Host(u.Hostname()))
}
