package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
)

// remoteTimeout — сколько ждать сервер и, отдельно, Связку ключей: агент не
// должен висеть на хосте без VPN.
const remoteTimeout = 3 * time.Second

// tokenGetter reads a server's token: Связка ключей, в тестах — память.
type tokenGetter interface {
	Get(account string) (string, error)
}

// remoteServers is mcpserver.RemoteSearcher over the kb servers of the
// window's config (--files-config): те же серверы, тот же токен в Связке, что
// у окна, — агент видит то, что видит человек.
//
// ponytail: четыре среза угла, все — в следующую работу.
//   - Гонка токена: окно пишет новый токен в Связку до записи конфига, и
//     поиск агента ровно в этот момент прочтет старый адрес и новый токен —
//     токен уйдет старому хосту. Окно от этого защищено снимком настроек, MCP —
//     другой процесс. Путь: межпроцессная блокировка config.json (filelock) на
//     время сохранения в окне и чтения здесь.
//   - Свой каталог (--catalog) всегда первым: позиция и выключение каталога в
//     списке окна, вторые каталоги здесь не учитываются.
//   - Токен читается из Связки на каждый вызов, без кэша окна (cachedTokens).
//   - Разбор записи сервера и чтение токена в сроке повторяют окно (desktop
//     typedEntries, tokenWithin). Путь: общий пакет источников.
type remoteServers struct {
	cfgPath string
	tokens  tokenGetter
}

// SearchRemote asks every enabled server in list order, in parallel.
// Конфиг читается на каждый вызов: окно меняет список без перезапуска MCP.
func (r remoteServers) SearchRemote(ctx context.Context, q string) []mcpserver.RemoteFound {
	cfg, err := desktopconfig.Read(r.cfgPath)
	if err != nil {
		return []mcpserver.RemoteFound{{Source: mcpserver.RemoteSource{ID: "config"}, Err: "конфиг окна не прочитался: " + err.Error()}}
	}
	var servers []desktopconfig.Source
	for _, s := range cfg.WithSources().Sources {
		if s.Enabled && s.Kind == desktopconfig.KindServer {
			servers = append(servers, s)
		}
	}
	out := make([]mcpserver.RemoteFound, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() { out[i] = r.ask(ctx, s, q) })
	}
	wg.Wait()
	return out
}

// ask searches one server with its stored token.
func (r remoteServers) ask(ctx context.Context, s desktopconfig.Source, q string) mcpserver.RemoteFound {
	found := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: s.ID, Name: s.Name, URL: s.URL}}
	tok, err := r.token(ctx, s.ID)
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		found.Err = "Связка ключей не ответила: разблокируйте ее"
		return found
	}
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	res, err := kbserver.Client{URL: s.URL, Token: tok}.Search(ctx, q, mcpserver.RemoteLimit)
	if err != nil {
		found.Err = kbserver.Describe(err)
		return found
	}
	found.Entries, found.Keys = res.Entries, res.Keys
	return found
}

// token reads the token within remoteTimeout: Связка, ждущая разблокировки,
// не держит ответ агенту.
func (r remoteServers) token(ctx context.Context, id string) (string, error) {
	type res struct {
		tok string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		t, err := r.tokens.Get(id)
		ch <- res{t, err}
	}()
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	select {
	case v := <-ch:
		return v.tok, v.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
