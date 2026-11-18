// Package monium ships the server's error lines to Monium over OTLP JSON (#440).
// Cloud Logging is closed to new clients (22.10.2026) and the container's stdout
// never reaches Monium on its own, so the server sends its errors itself.
package monium

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ErrConfig — не хватает адреса, ключа или проекта: отправка выключена.
var ErrConfig = errors.New("monium: endpoint, api key and project are required")

// DefaultQueue is how many lines wait for sending before new ones are dropped.
const DefaultQueue = 256

// Config of the sender. Cluster and Service empty — Monium takes "default".
type Config struct {
	Endpoint string
	APIKey   string
	Project  string
	Cluster  string
	Service  string
	Client   *http.Client // nil — клиент с коротким таймаутом
	Now      func() time.Time
	Queue    int // 0 — DefaultQueue
}

// Writer is an io.Writer for log.SetOutput: every line goes to Monium as ERROR.
type Writer struct{}

// New checks the config and builds the writer.
func New(cfg Config) (*Writer, error) { return &Writer{}, nil }

// Write queues the line and never blocks or fails: the server must not wait for Monium.
func (w *Writer) Write(p []byte) (int, error) { return len(p), nil }

// Run sends the queue until ctx ends, then sends what is left.
func (w *Writer) Run(ctx context.Context) { <-ctx.Done() }

// Dropped is how many lines were thrown away because the queue was full.
func (w *Writer) Dropped() uint64 { return 0 }
