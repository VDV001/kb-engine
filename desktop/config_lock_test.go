//go:build unix

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

// Связка ключей меняется только под замком config.json: MCP-сервер, другой
// процесс, читает адрес и токен под тем же замком и не видит новый токен при
// старом адресе (запись) и старый токен при новом адресе (удаление).
func TestSetSources_TokensChangeUnderConfigLock(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken}); err != nil {
		t.Fatal(err)
	}
	touched := 0
	tok.touch = func() {
		touched++
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := filelock.WithContext(ctx, s.path, func() error { return nil }); !errors.Is(err, filelock.ErrBusy) {
			t.Errorf("Связка меняется без замка конфига: %v", err)
		}
	}
	// Новый токен — запись; новый адрес без токена — удаление старого.
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb2.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken + "2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb3.example.ru", true), nil, nil); err != nil {
		t.Fatal(err)
	}
	if touched != 2 {
		t.Fatalf("Связка тронута %d раз, want 2 (запись и удаление)", touched)
	}
}
