package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Сервер распознавания: подключение в настройках, ключ в Связке; выбран в
// «Слушает» — диктовка отдает запись ему, строка подключения это говорит.
func TestSpeechConnection(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k-1" || r.URL.Path != "/v1/audio/transcriptions" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"text":"срок поставки"}`)
	}))
	defer srv.Close()
	// Адрес httptest — 127.0.0.1: ключ на этот Мак уходит.
	row, err := s.AddSpeech("whisper.cpp", srv.URL+"/v1/", "large-v3", "k-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "speech" || !row.OK || row.Status != "ключ в Связке ключей" || !strings.Contains(row.Detail, "large-v3") {
		t.Fatalf("row = %+v", row)
	}
	id := row.ID
	if tok.m["conn:"+id+":key"] != "k-1" {
		t.Fatalf("ключ: %v", tok.m)
	}
	if tr, err := s.listenTranscriber(); tr != nil || err != nil {
		t.Fatalf("без выбора — Apple Speech: %v, %v", tr != nil, err)
	}
	_ = s.updateConfig(func(c *desktopconfig.Config) { c.Chat.Listen = id })
	tr, err := s.listenTranscriber()
	if err != nil || tr == nil {
		t.Fatalf("transcriber: %v", err)
	}
	if text, err := tr(context.Background(), []byte("RIFF")); err != nil || text != "срок поставки" {
		t.Fatalf("text %q, %v", text, err)
	}
	rows := s.Connections()
	if last := rows[len(rows)-1]; strings.Join(last.UsedBy, ",") != "диктовка в чате" {
		t.Fatalf("row = %+v", last)
	}
	// Выключено — диктовка говорит, почему не слушает.
	_ = s.updateConfig(func(c *desktopconfig.Config) { c.Connections[0].Enabled = false })
	if _, err := s.listenTranscriber(); err == nil || !strings.Contains(err.Error(), "выключено") {
		t.Fatalf("выключенное: %v", err)
	}
	if _, err := s.AddSpeech("x", "http://192.0.2.1:8080/v1", "", ""); err == nil {
		t.Fatal("http в сеть принят")
	}
}
