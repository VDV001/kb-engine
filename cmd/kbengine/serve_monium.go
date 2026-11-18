package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/monium"
)

// moniumEndpoint is Monium's OTLP logs address; KB_MONIUM_ENDPOINT replaces it (tests, a region).
const moniumEndpoint = "https://ingest.monium.yandex.cloud/otlp/v1/logs"

// moniumFromEnv builds the Monium sender from KB_MONIUM_* (#440). Ничего не задано —
// (nil, nil): отправка выключена. Одна из пары KB_MONIUM_API_KEY и KB_MONIUM_PROJECT
// без другой — ошибка с именем переменной, а не молчаливо выключенные логи.
func moniumFromEnv(getenv func(string) string) (*monium.Writer, error) {
	key, project := getenv("KB_MONIUM_API_KEY"), getenv("KB_MONIUM_PROJECT")
	if key == "" && project == "" {
		return nil, nil
	}
	if key == "" {
		return nil, fmt.Errorf("KB_MONIUM_API_KEY: required when KB_MONIUM_PROJECT is set")
	}
	if project == "" {
		return nil, fmt.Errorf("KB_MONIUM_PROJECT: required when KB_MONIUM_API_KEY is set")
	}
	endpoint := getenv("KB_MONIUM_ENDPOINT")
	if endpoint == "" {
		endpoint = moniumEndpoint
	}
	if !strings.HasPrefix(endpoint, "https://") { // ключ в заголовке: по http он ушел бы открытым текстом
		return nil, fmt.Errorf("KB_MONIUM_ENDPOINT: must start with https://")
	}
	service := getenv("KB_MONIUM_SERVICE")
	if service == "" {
		service = "kb-server"
	}
	return monium.New(monium.Config{Endpoint: endpoint, APIKey: key, Project: project, Service: service})
}

// startMonium turns the sender on when KB_MONIUM_* is set: ошибки сервера идут и в
// stderr, и в Monium (stdout контейнера туда сам не попадает, #440). Возвращает
// писатель для сообщений сервера и stop, который досылает остаток очереди.
func startMonium(getenv func(string) string, stderr io.Writer) (io.Writer, func(), error) {
	mon, err := moniumFromEnv(getenv)
	if err != nil || mon == nil {
		return stderr, func() {}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); mon.Run(ctx) }()
	out := io.MultiWriter(mon, stderr) // mon первым: он не возвращает ошибок, и сбой stderr не отнимает строку у Monium
	log.SetOutput(out)
	return out, func() {
		cancel()
		<-done
		log.SetOutput(stderr) // писатель остановлен: строки после выхода не должны уходить в пустоту
	}, nil
}
