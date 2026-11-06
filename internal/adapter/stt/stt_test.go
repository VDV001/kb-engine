package stt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/stt"
)

// Распознавание — OpenAI-совместимый /audio/transcriptions: так отвечают
// whisper.cpp server, faster-whisper и OpenAI. Запись уходит multipart с
// моделью и языком; ключ — только если задан.
func TestTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer k1" {
			t.Errorf("запрос %s, auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		if string(b) != "RIFF-wav" || h.Filename != "speech.wav" || r.FormValue("model") != "whisper-1" || r.FormValue("language") != "ru" {
			t.Errorf("форма: %q %q %q %q", b, h.Filename, r.FormValue("model"), r.FormValue("language"))
		}
		_, _ = io.WriteString(w, `{"text":" Срок поставки? "}`)
	}))
	defer srv.Close()
	got, err := stt.Client{BaseURL: srv.URL + "/v1", Key: "k1", Model: "whisper-1", HTTP: srv.Client()}.
		Transcribe(context.Background(), []byte("RIFF-wav"), "ru")
	if err != nil || got != "Срок поставки?" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Отказы — свои; ключ на чужой http не уходит; ключ не попадает в ошибку.
func TestTranscribe_Refusals(t *testing.T) {
	for code, want := range map[int]error{401: stt.ErrUnauthorized, 404: stt.ErrNotFound, 500: stt.ErrServer} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, "secret-key")
		}))
		_, err := stt.Client{BaseURL: srv.URL, Key: "secret-key", HTTP: srv.Client()}.Transcribe(context.Background(), []byte("x"), "ru")
		srv.Close()
		if !errors.Is(err, want) || strings.Contains(err.Error(), "secret-key") {
			t.Errorf("%d: %v", code, err)
		}
	}
	if _, err := (stt.Client{BaseURL: "http://192.0.2.1:8080/v1", Key: "k"}).Transcribe(context.Background(), []byte("x"), "ru"); !errors.Is(err, stt.ErrInsecureURL) {
		t.Fatalf("ключ на чужой http: %v", err)
	}
}
