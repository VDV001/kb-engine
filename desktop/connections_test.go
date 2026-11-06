package main

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// «Подключения» собирают доступы, которые уже есть: модель чата и kb-серверы.
// У каждого — где он живет, есть ли ключ и кто им пользуется; ключ сам не
// показывается никогда.
func TestConnections(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	list := append(serverList("srv1", "https://kb.example.com", true),
		desktopconfig.Source{ID: "srv2", Kind: desktopconfig.KindServer, URL: "https://down.example.com", Name: "Офис", Enabled: false})
	if err := s.SetSources(list, nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	tok.m[chatKeyAccount] = "not-a-real-key"
	s.cfg.Chat = desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "anthropic/claude-sonnet-5.5"}

	rows := s.Connections()
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	m := rows[0]
	if m.Kind != "model" || m.Name != "Vercel AI Gateway" || !m.OK || m.Section != "chat" || !strings.Contains(m.Detail, "claude-sonnet-5.5") || strings.Join(m.UsedBy, ",") != "модель чата" {
		t.Fatalf("модель: %+v", m)
	}
	a, b := rows[1], rows[2]
	if a.Kind != "kbserver" || a.Name != "kb.example.com" || !a.OK || a.Status != "токен в Связке ключей" || a.Section != "sources" || a.UsedBy[0] != "источник «kb.example.com»" {
		t.Fatalf("сервер: %+v", a)
	}
	if b.Name != "Офис" || b.OK || b.Status != "выключен" {
		t.Fatalf("выключенный сервер: %+v", b)
	}
	for _, r := range rows {
		if strings.Contains(r.Detail+r.Status, "not-a-real-key") || strings.Contains(r.Detail+r.Status, srvToken) {
			t.Fatalf("ключ попал в строку: %+v", r)
		}
	}

	// Модель на этом Маке ключа не требует; облачная без ключа — не готова.
	s.cfg.Chat = desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}
	if r := s.Connections()[0]; r.Name != "Ollama на этом Маке" || !r.OK || r.Status != "ключ не нужен" {
		t.Fatalf("ollama: %+v", r)
	}
	delete(tok.m, chatKeyAccount)
	s.cfg.Chat = desktopconfig.Chat{URL: "https://api.example.com/v1", Model: "m"}
	if r := s.Connections()[0]; r.Name != "api.example.com" || r.OK || r.Status != "нет ключа" {
		t.Fatalf("без ключа: %+v", r)
	}
}
