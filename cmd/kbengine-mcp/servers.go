package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
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
// Адрес и токен читаются одной парой под замком config.json, тем же, под
// которым окно меняет Связку и конфиг (desktop SetSources): иначе поиск в
// момент сохранения прочел бы старый адрес и новый токен, и токен ушел бы
// старому хосту. Сеть — уже без замка: сохранение в окне не ждет серверы.
//
// ponytail: три среза угла, все — в следующую работу.
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
	servers, err := r.snapshot(ctx)
	switch {
	case errors.Is(err, context.Canceled):
		return nil // агент отменил запрос: ответ уже никто не читает
	case errors.Is(err, filelock.ErrBusy):
		return []mcpserver.RemoteFound{{Source: mcpserver.RemoteSource{ID: "config"}, Err: "окно сохраняет источники, серверы не спрошены: повторите поиск"}}
	case err != nil:
		return []mcpserver.RemoteFound{{Source: mcpserver.RemoteSource{ID: "config"}, Err: "конфиг окна не прочитался: " + err.Error()}}
	}
	out := make([]mcpserver.RemoteFound, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() { out[i] = r.ask(ctx, s, q) })
	}
	wg.Wait()
	return out
}

// withToken is an enabled server and what the Связка gave for it.
type withToken struct {
	src desktopconfig.Source
	tok string
	err error
}

// snapshot reads the enabled servers and their tokens under the config lock,
// waiting for it no longer than remoteTimeout. У ожидания замка свой срок, у
// Связки свой (token): окно, державшее замок почти весь срок, не превращает
// разблокированную Связку в «не ответила».
func (r remoteServers) snapshot(ctx context.Context) ([]withToken, error) {
	waitCtx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	var out []withToken
	err := filelock.WithContext(waitCtx, r.cfgPath, func() error {
		cfg, err := desktopconfig.Read(r.cfgPath)
		if err != nil {
			return err
		}
		for _, s := range cfg.WithSources().Sources {
			if s.Enabled && s.Kind == desktopconfig.KindServer {
				out = append(out, withToken{src: s})
			}
		}
		var wg sync.WaitGroup
		for i := range out {
			wg.Go(func() { out[i].tok, out[i].err = r.token(ctx, out[i].src.ID) })
		}
		wg.Wait()
		return nil
	})
	return out, err
}

// ask searches one server with the token read in its snapshot.
func (r remoteServers) ask(ctx context.Context, w withToken, q string) mcpserver.RemoteFound {
	s, tok := w.src, w.tok
	found := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: s.ID, Name: s.Name, URL: s.URL}}
	if w.err != nil && !errors.Is(w.err, keychain.ErrNotFound) {
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
