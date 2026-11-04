//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"strings"
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

// Адрес сменили без нового токена, а Связка не дала удалить старый: конфиг
// с новым адресом не пишется, иначе MCP прочел бы под замком новый адрес и
// старый токен одной парой и отправил токен новому хосту.
func TestSetSources_DeleteRefusedKeepsOldAddress(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken}); err != nil {
		t.Fatal(err)
	}
	tok.delErr = errors.New("keychain locked")
	if err := s.SetSources(serverList("server-ab12cd34", "https://evil.example.ru", true), nil, nil); err == nil {
		t.Fatal("сохранено при отказе удаления старого токена")
	}
	raw, _ := os.ReadFile(s.path)
	if strings.Contains(string(raw), "evil.example.ru") || !strings.Contains(string(raw), "kb.example.ru") {
		t.Fatalf("config.json = %s", raw)
	}
	if tok.m["server-ab12cd34"] != srvToken {
		t.Fatalf("Связка = %v", tok.m)
	}
}

// Пока окно ждет замок, который держит MCP, поиск окна не теряет серверы:
// «сохраняется» публикуется, только когда замок взят.
func TestSetSources_WaitingForLockKeepsSearch(t *testing.T) {
	s, _, _ := newServerSettings(t)
	s.publish() // как main: снимок есть до первого поиска, snapshot не берет f.mu
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = filelock.With(s.path, func() error { close(held); <-release; return nil })
	}()
	<-held
	done := make(chan error, 1)
	go func() {
		done <- s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken})
	}()
	time.Sleep(100 * time.Millisecond)
	saving := s.snapshot().saving
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if saving {
		t.Fatal("поиск окна пропускал серверы, пока сохранение ждало замок")
	}
}
