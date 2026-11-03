package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// errBadToken — токен пришел для id, которого нет среди серверов списка:
// ошибка окна, а не повод положить в Связку ключей чужую запись.
var errBadToken = errors.New("токен для источника, которого нет среди серверов")

// tokenStore — где лежат токены серверов: Связка ключей macOS, в тестах — память.
type tokenStore interface {
	Set(account, secret string) error
	Get(account string) (string, error)
	Delete(account string) error
}

// probeFunc asks a server for its version and entry count.
type probeFunc func(ctx context.Context, url, token string) (kbserver.Info, error)

// probeTimeout — сколько ждать сервер на экране источников: панель не висит
// из-за одного недоступного хоста, опросы идут параллельно.
const probeTimeout = 3 * time.Second

// storeTokens puts the tokens of next's servers into the store. Если Связка
// отказала посередине, уже записанное возвращается как было: конфиг тогда не
// пишется, и токены не должны разойтись с ним.
func (f *folderSettings) storeTokens(next desktopconfig.Config, tokens map[string]string) error {
	servers := map[string]bool{}
	for _, s := range next.Sources {
		if s.Kind == desktopconfig.KindServer {
			servers[s.ID] = true
		}
	}
	for id := range tokens {
		if !servers[id] {
			return fmt.Errorf("%w: %s", errBadToken, id)
		}
	}
	type prev struct {
		val string
		had bool
	}
	done := map[string]prev{}
	for id, tok := range tokens {
		old, err := f.tokens.Get(id)
		done[id] = prev{old, err == nil}
		if err := f.tokens.Set(id, tok); err != nil {
			for rid, p := range done {
				if p.had {
					_ = f.tokens.Set(rid, p.val)
				} else {
					_ = f.tokens.Delete(rid)
				}
			}
			return fmt.Errorf("токен не сохранен в Связке ключей: %w", err)
		}
	}
	return nil
}

// dropTokens removes the tokens of servers that left the list.
func (f *folderSettings) dropTokens(prev, next desktopconfig.Config) {
	kept := map[string]bool{}
	for _, s := range next.Sources {
		kept[s.ID] = true
	}
	for _, s := range prev.WithSources().Sources {
		if s.Kind == desktopconfig.KindServer && !kept[s.ID] {
			_ = f.tokens.Delete(s.ID) // не удалось — останется лишняя запись, не утечка
		}
	}
}

// token returns the stored token of a server, "" if there is none.
func (f *folderSettings) token(id string) string {
	t, err := f.tokens.Get(id)
	if err != nil {
		return ""
	}
	return t
}

// fillServers asks the enabled servers in parallel and fills their rows.
func (f *folderSettings) fillServers(rows []sourceRow) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		if r.Kind != desktopconfig.KindServer {
			continue
		}
		tok := f.token(r.ID)
		r.HasToken = tok != ""
		if !r.Enabled {
			continue
		}
		wg.Go(func() {
			info, err := f.probe(ctx, r.URL, tok)
			switch {
			case err == nil:
				r.State, r.Count, r.Note = stateOK, info.Entries, "kbengine "+info.Version
			case errors.Is(err, kbserver.ErrUnauthorized):
				r.State, r.Note = stateMissing, "сервер ответил 401: нужен верный токен"
			case errors.Is(err, kbserver.ErrNotKB):
				r.State, r.Note = stateMissing, "по адресу отвечает не kb-сервер"
			default:
				r.State, r.Note = stateMissing, "нет связи: проверьте адрес и VPN"
			}
		})
	}
	wg.Wait()
}

// CheckServer is the «Проверить» button: the address passes the same rule as
// on save (http only to this Mac), the token is the one typed in the form or,
// if empty, the one stored for this id.
func (f *folderSettings) CheckServer(id, url, token string) (kbserver.Info, error) {
	probe := desktopconfig.Config{Sources: []desktopconfig.Source{{ID: "check", Kind: desktopconfig.KindServer, URL: url, Enabled: true}}}
	if err := probe.Validate(); err != nil {
		return kbserver.Info{}, err
	}
	if token == "" {
		token = f.token(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return f.probe(ctx, url, token)
}

// keychainTokens is the production token store.
func keychainTokens() tokenStore { return keychain.Store{Service: "kb-desktop"} }

// probeServer asks a real kb server; таймаут задает контекст вызывающего.
func probeServer(ctx context.Context, url, token string) (kbserver.Info, error) {
	return kbserver.Client{URL: url, Token: token}.Info(ctx)
}
