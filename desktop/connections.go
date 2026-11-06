package main

import (
	"errors"
	"net/url"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// «Подключения» (решение владельца 06.11, вариант А): куда и с каким доступом
// ходит окно. Пока строки собираются из доступов, у которых уже есть дом —
// модель чата и kb-серверы, — и раздел показывает их в одном месте, ведя
// править туда, где они живут.
//
// ponytail: своего хранилища у подключений нет. Потолок: доступ без
// источника (вход Google, сервер распознавания речи) показать негде. Путь:
// список подключений в config.json, секреты — в Связке по id подключения;
// источники и чат ссылаются на id.

// connRow is one access the window holds. Ключ и токен сюда не попадают —
// только признак, что они есть.
type connRow struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // model | kbserver
	Name    string   `json:"name"`
	Detail  string   `json:"detail"`
	Status  string   `json:"status"`
	OK      bool     `json:"ok"`
	UsedBy  []string `json:"usedBy"`
	Section string   `json:"section"` // раздел настроек, где подключение правится
}

// Connections lists the window's accesses: модель чата, затем kb-серверы в
// порядке источников.
func (f *folderSettings) Connections() []connRow {
	f.mu.Lock()
	cfg := f.cfg
	f.mu.Unlock()
	rows := []connRow{f.modelConn(cfg.Chat.WithDefaults())}
	for _, s := range cfg.WithSources().Sources {
		if s.Kind == desktopconfig.KindServer {
			rows = append(rows, f.serverConn(s))
		}
	}
	return rows
}

func (f *folderSettings) modelConn(c desktopconfig.Chat) connRow {
	r := connRow{ID: "chat-model", Kind: "model", Name: modelProvider(c), Detail: c.URL + " · " + c.Model,
		UsedBy: []string{"модель чата"}, Section: "chat"}
	switch _, err := f.tokens.Get(chatKeyAccount); {
	case c.Local():
		r.Status, r.OK = "ключ не нужен", true
	case err == nil:
		r.Status, r.OK = "ключ в Связке ключей", true
	case !errors.Is(err, keychain.ErrNotFound):
		r.Status = keychainNote
	default:
		r.Status = "нет ключа"
	}
	return r
}

func (f *folderSettings) serverConn(s desktopconfig.Source) connRow {
	name := sourceLabel(s)
	r := connRow{ID: s.ID, Kind: "kbserver", Name: name, Detail: s.URL,
		UsedBy: []string{"источник «" + name + "»"}, Section: "sources"}
	_, err := f.tokens.Get(s.ID)
	switch {
	case !s.Enabled:
		r.Status = "выключен"
	case err == nil:
		r.Status, r.OK = "токен в Связке ключей", true
	case !errors.Is(err, keychain.ErrNotFound):
		r.Status = keychainNote
	default:
		r.Status = "нет токена"
	}
	return r
}

// modelProvider names who answers: знакомые адреса — по имени, остальные — по хосту.
func modelProvider(c desktopconfig.Chat) string {
	u, err := url.Parse(c.URL)
	switch {
	case err != nil || u.Host == "":
		return c.URL
	case c.Local() && u.Port() == "11434":
		return "Ollama на этом Маке"
	case c.Local():
		return "Модель на этом Маке"
	case strings.HasSuffix(u.Hostname(), "ai-gateway.vercel.sh"):
		return "Vercel AI Gateway"
	}
	return u.Hostname()
}
