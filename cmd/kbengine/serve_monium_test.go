package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMoniumFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantOn  bool
		wantErr string // подстрока ошибки; пусто — без ошибки
	}{
		{"nothing set: off", nil, false, ""},
		{"key without project", map[string]string{"KB_MONIUM_API_KEY": "k"}, false, "KB_MONIUM_PROJECT"},
		{"project without key", map[string]string{"KB_MONIUM_PROJECT": "folder__x"}, false, "KB_MONIUM_API_KEY"},
		{"plain http endpoint would leak the key", map[string]string{
			"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x", "KB_MONIUM_ENDPOINT": "http://ingest.example/otlp/v1/logs",
		}, false, "KB_MONIUM_ENDPOINT"},
		{"key and project: on", map[string]string{"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x"}, true, ""},
		{"custom https endpoint: on", map[string]string{
			"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x", "KB_MONIUM_ENDPOINT": "https://ingest.example/otlp/v1/logs",
		}, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, err := moniumFromEnv(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to name %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (w != nil) != tc.wantOn {
				t.Errorf("writer on = %v, want %v", w != nil, tc.wantOn)
			}
		})
	}
}

func TestStartMonium(t *testing.T) {
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	t.Run("off: stderr as is, stop is harmless", func(t *testing.T) {
		var buf bytes.Buffer
		out, stop, err := startMonium(func(string) string { return "" }, &buf)
		if err != nil || out != io.Writer(&buf) {
			t.Fatalf("out %v err %v", out, err)
		}
		stop()
	})
	t.Run("bad config is an error, not silent", func(t *testing.T) {
		_, _, err := startMonium(func(k string) string { return map[string]string{"KB_MONIUM_API_KEY": "k"}[k] }, io.Discard)
		if err == nil {
			t.Fatal("want an error naming KB_MONIUM_PROJECT")
		}
	})
	t.Run("on: lines still reach stderr and the standard log", func(t *testing.T) {
		var buf bytes.Buffer
		env := map[string]string{"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x", "KB_MONIUM_ENDPOINT": "https://127.0.0.1:1/otlp/v1/logs"}
		out, stop, err := startMonium(func(k string) string { return env[k] }, &buf)
		if err != nil {
			t.Fatal(err)
		}
		log.SetFlags(0)
		log.Print("portrait read: boom")
		fmt.Fprintln(out, "serve: warning")
		stop()
		if got := buf.String(); !strings.Contains(got, "portrait read: boom") || !strings.Contains(got, "serve: warning") {
			t.Errorf("stderr lost lines: %q", got)
		}
		if log.Writer() != io.Writer(&buf) { // после stop строки не должны уходить в Writer, который никто не читает
			t.Error("stop must put the standard log back on stderr")
		}
		// Адрес недоступен (127.0.0.1:1), две строки не дошли: об этом остается след в stderr (#697).
		if !strings.Contains(buf.String(), "log lines never reached Monium") {
			t.Errorf("stop must say how many lines were lost, stderr: %q", buf.String())
		}
	})
}

// По SIGTERM платформа ждет считанные секунды: сервер обязан вернуться из serve,
// иначе отложенная досылка очереди Monium не выполнится (ревью #696).
func TestListenAndServeUntilStopsOnContext(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- listenAndServeUntil(ctx, srv) }()
	time.Sleep(100 * time.Millisecond) // дать серверу подняться
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stop by context is not an error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = srv.Close() // чтобы горутина не осталась
		t.Fatal("server kept running after the context ended")
	}
}

// Shutdown возвращается, когда запросы закончились; если выйти раньше, процесс оборвет
// запрос в работе (ревью #696, круг 2: функция возвращалась через 30 мкс).
func TestListenAndServeUntilWaitsForRequestsInFlight(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	started := make(chan struct{})
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(700 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})}
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan time.Time, 1)
	go func() { _ = listenAndServeUntil(ctx, srv); returned <- time.Now() }()

	body := make(chan string, 1)
	go func() {
		for range 50 { // сервер поднимается не мгновенно
			resp, err := http.Get("http://" + addr + "/")
			if err != nil {
				time.Sleep(40 * time.Millisecond)
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			body <- string(b)
			return
		}
		body <- "no response"
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the handler")
	}
	cancelAt := time.Now()
	cancel()
	select {
	case at := <-returned:
		if d := at.Sub(cancelAt); d < 500*time.Millisecond {
			t.Errorf("returned %v after the stop, while the request needs 700 ms: in-flight requests are cut", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return")
	}
	if got := <-body; got != "done" {
		t.Errorf("response %q, want done", got)
	}
}
