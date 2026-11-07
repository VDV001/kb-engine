package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/stt"
)

// Сервер распознавания речи — подключение (решение владельца 06.11: «если
// хочу whisper подключить — должен смочь в интерфейсе»): адрес
// OpenAI-совместимого /audio/transcriptions, модель, ключ — в Связке.

func speechKeyAccount(id string) string { return "conn:" + id + ":key" }

// AddSpeech saves a speech recognition server as a connection.
func (f *folderSettings) AddSpeech(name, url, model, key string) (connRow, error) {
	name, url, model, key = strings.TrimSpace(name), strings.TrimSpace(url), strings.TrimSpace(model), strings.TrimSpace(key)
	if name == "" {
		name = "Распознавание речи"
	}
	conn := desktopconfig.Connection{ID: "speech-" + randomString()[:8], Kind: desktopconfig.ConnSpeech, Name: name, URL: strings.TrimRight(url, "/"), Model: model, Enabled: true}
	if err := desktopconfig.CheckConnections([]desktopconfig.Connection{conn}); err != nil {
		return connRow{}, err
	}
	if key != "" {
		if err := f.tokens.Set(speechKeyAccount(conn.ID), key); err != nil {
			return connRow{}, fmt.Errorf("ключ не сохранен в Связке ключей: %w", err)
		}
	}
	if err := f.updateConfig(func(cfg *desktopconfig.Config) { cfg.Connections = append(cfg.Connections, conn) }); err != nil {
		_ = f.tokens.Delete(speechKeyAccount(conn.ID))
		return connRow{}, err
	}
	return f.speechConn(conn), nil
}

func (f *folderSettings) speechConn(c desktopconfig.Connection) connRow {
	r := connRow{ID: c.ID, Kind: "speech", Name: c.Name, Detail: c.URL + " · " + or(c.Model, "whisper-1"),
		Section: "conn", Own: true, Enabled: c.Enabled}
	if f.cfg.Chat.Listen == c.ID {
		r.UsedBy = []string{"диктовка в чате"}
	}
	_, err := f.tokens.Get(speechKeyAccount(c.ID))
	switch {
	case !c.Enabled:
		r.Status = "выключено"
	case err == nil:
		r.Status, r.OK = "ключ в Связке ключей", true
	case !errors.Is(err, keychain.ErrNotFound):
		r.Status = keychainNote
	default:
		r.Status, r.OK = "без ключа", true
	}
	return r
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// listenTranscriber returns who transcribes dictation: подключение из
// «Слушает» или nil — Apple Speech.
func (f *folderSettings) listenTranscriber() (transcriber, error) {
	cfg := f.snapshot().cfg
	id := cfg.Chat.Listen
	if id == "" {
		return nil, nil
	}
	if model, ok := cloudListen[id]; ok {
		cloud := cloudSpeech{base: gatewayAudio, client: &http.Client{Timeout: 60 * time.Second}, key: f.gatewayKey, calls: f.speechCalls}
		return func(ctx context.Context, wav []byte) (string, error) { return cloud.transcribe(ctx, wav, model) }, nil
	}
	c, ok := cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnSpeech {
		return nil, errors.New("сервер распознавания из «Слушает» удален — выберите другой в «Чат и модель»")
	}
	if !c.Enabled {
		return nil, fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(speechKeyAccount(id))
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return nil, keychainNote2(err)
	}
	client := stt.Client{BaseURL: c.URL, Key: key, Model: c.Model}
	return func(ctx context.Context, wav []byte) (string, error) {
		text, err := client.Transcribe(ctx, wav, "ru")
		if err != nil {
			return "", speechErr(c.Name, err)
		}
		return text, nil
	}, nil
}

// gatewayKey returns the chat key when the chat goes through Vercel AI
// Gateway; ключ другого провайдера на шлюз не уходит.
func (f *folderSettings) gatewayKey() (string, error) {
	u, err := url.Parse(f.snapshot().cfg.Chat.WithDefaults().URL)
	if err != nil || u.Hostname() != "ai-gateway.vercel.sh" {
		return "", errors.New("облачный голос и распознавание работают через Vercel AI Gateway: в «Модель» нужен адрес шлюза и его ключ")
	}
	key, err := f.tokens.Get(chatKeyAccount)
	if err != nil {
		return "", errors.New("нет ключа шлюза — введите его в «Модель»")
	}
	return key, nil
}

func keychainNote2(err error) error { return humanErr{keychainNote, err} }

// speechErr names a speech server refusal in words.
func speechErr(name string, err error) error {
	switch {
	case errors.Is(err, stt.ErrUnauthorized):
		return fmt.Errorf("%s не принял ключ — проверьте его в «Подключениях»", name)
	case errors.Is(err, stt.ErrNotFound):
		return fmt.Errorf("%s: по адресу нет распознавания речи (/audio/transcriptions) — у whisper-server включите --inference-path /v1/audio/transcriptions", name)
	case errors.Is(err, stt.ErrInsecureURL):
		return errors.New("ключ уходит только по https или на этот Мак")
	}
	return fmt.Errorf("%s не ответил: %w", name, err)
}
