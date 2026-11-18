package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"
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
	})
}
