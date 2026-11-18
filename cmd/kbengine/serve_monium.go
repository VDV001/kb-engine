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

// moniumRun is the running sender: where the server writes its messages, how many log
// lines never reached Monium (nil — отправка выключена) and how to stop it.
type moniumRun struct {
	out     io.Writer
	dropped func() uint64
	stop    func()
}

// tenantOptions tells /healthz about the lost lines when the sender is on.
func (m moniumRun) tenantOptions() []tenantOption {
	if m.dropped == nil {
		return nil
	}
	return []tenantOption{withLogDropped(m.dropped)}
}

// startMonium turns the sender on when KB_MONIUM_* is set: ошибки сервера идут и в
// stderr, и в Monium (stdout контейнера туда сам не попадает, #440). stop досылает
// остаток очереди и говорит, сколько строк не дошло. Строка-маркер старта идет тем же
// путем, что любая ошибка, поэтому каждая выкатка сама показывает в Monium, что ключ,
// сеть и отправка работают (#697).
func startMonium(getenv func(string) string, stderr io.Writer) (moniumRun, error) {
	mon, err := moniumFromEnv(getenv)
	if err != nil || mon == nil {
		return moniumRun{out: stderr, stop: func() {}}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); mon.Run(ctx) }()
	out := io.MultiWriter(mon, stderr) // mon первым: он не возвращает ошибок, и сбой stderr не отнимает строку у Monium
	log.SetOutput(out)
	log.Print("serve: logs are shipped to Monium")
	return moniumRun{out: out, dropped: mon.Dropped, stop: func() {
		cancel()
		<-done
		log.SetOutput(stderr) // писатель остановлен: строки после выхода не должны уходить в пустоту
		if n := mon.Dropped(); n > 0 {
			fmt.Fprintf(stderr, "serve: %d log lines never reached Monium\n", n)
		}
	}}, nil
}
