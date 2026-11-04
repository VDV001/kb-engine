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

// stateChecking — сервер в списке, его состояние догружает ServerStates:
// панель открывается сразу, не дожидаясь сети и Связки ключей.
const stateChecking = "checking"

// checkTokenIDs refuses a token for an id that is not a server of next.
func checkTokenIDs(next desktopconfig.Config, tokens map[string]string) error {
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
	return nil
}

// storeTokens checks that every token belongs to a server of next and writes
// them, returning the undo: SetSources зовет его, если после токенов отказали
// apply или запись конфига, — Связка не расходится с конфигом. Сбой чтения
// Связки (заперта, отказ в доступе) — отказ сразу: принять его за «токена нет»
// значило бы при откате стереть рабочий токен.
func (f *folderSettings) storeTokens(next desktopconfig.Config, tokens map[string]string) (func(), error) {
	if err := checkTokenIDs(next, tokens); err != nil {
		return nil, err
	}
	type prev struct {
		val string
		had bool
	}
	done := map[string]prev{}
	undo := func() {
		for id, p := range done {
			if p.had {
				_ = f.tokens.Set(id, p.val)
			} else {
				_ = f.tokens.Delete(id)
			}
		}
	}
	for id, tok := range tokens {
		old, err := f.tokens.Get(id)
		switch {
		case err == nil:
			done[id] = prev{old, true}
		case errors.Is(err, keychain.ErrNotFound):
			done[id] = prev{}
		default:
			undo()
			return nil, fmt.Errorf("связка ключей не прочиталась: %w", err)
		}
		if err := f.tokens.Set(id, tok); err != nil {
			undo()
			return nil, fmt.Errorf("токен не сохранен в Связке ключей: %w", err)
		}
	}
	return undo, nil
}

// dropTokens removes the token of a server that left the list, and of one
// whose address changed without a new token: старый токен не должен уйти
// новому хосту. Отказ удаления — отказ сохранения, а не лишняя запись: конфиг
// с новым адресом при старом токене и есть утечка. Возвращает откат.
func (f *folderSettings) dropTokens(prev, next desktopconfig.Config, tokens map[string]string) (func(), error) {
	urls := map[string]string{}
	for _, s := range next.Sources {
		urls[s.ID] = s.URL
	}
	dropped := map[string]string{}
	undo := func() {
		for id, tok := range dropped {
			_ = f.tokens.Set(id, tok)
		}
	}
	for _, s := range prev.WithSources().Sources {
		if s.Kind != desktopconfig.KindServer {
			continue
		}
		url, kept := urls[s.ID]
		_, fresh := tokens[s.ID]
		if kept && (url == s.URL || fresh) {
			continue
		}
		old, err := f.tokens.Get(s.ID)
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			continue
		case err != nil:
			undo()
			return nil, fmt.Errorf("связка ключей не прочиталась: %w", err)
		}
		if err := f.tokens.Delete(s.ID); err != nil {
			undo()
			return nil, fmt.Errorf("старый токен не удален из Связки ключей: %w", err)
		}
		dropped[s.ID] = old
	}
	return undo, nil
}

// token returns the stored token of a server, "" if there is none.
func (f *folderSettings) token(id string) string {
	t, err := f.tokens.Get(id)
	if err != nil {
		return ""
	}
	return t
}

// serverMessage is the one place where a server error becomes words for the
// screen: строка списка и «Проверить» говорят об одной беде одинаково.
func serverMessage(err error) string { return kbserver.Describe(err) }

// humanErr carries the screen text and keeps the cause for errors.Is.
type humanErr struct {
	msg string
	err error
}

func (e humanErr) Error() string { return e.msg }
func (e humanErr) Unwrap() error { return e.err }

// ServerStates asks every server of the list in parallel (≤ probeTimeout) and
// returns their rows. Отдельно от GetSources: панель рисует папки сразу, а
// серверы — когда ответят.
func (f *folderSettings) ServerStates() []sourceRow {
	f.mu.Lock()
	cfg := f.cfg
	f.mu.Unlock()
	var rows []sourceRow
	for _, s := range cfg.WithSources().Sources {
		if s.Kind == desktopconfig.KindServer {
			rows = append(rows, sourceRow{Source: s, State: stateOff})
		}
	}
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		wg.Go(func() {
			tok, err := f.tokens.Get(r.ID)
			switch {
			case err == nil:
				r.HasToken = true
			case !errors.Is(err, keychain.ErrNotFound):
				// Связка не ответила — это не «токена нет»: без токена сервер
				// не опрашивается, человеку не предлагается ввести его заново.
				r.HasToken = true
				if r.Enabled {
					r.State, r.Note = stateMissing, "Связка ключей не ответила: разблокируйте ее и откройте панель снова"
				}
				return
			}
			if !r.Enabled {
				return
			}
			// Срок опроса — с момента, когда токен уже прочитан: диалог
			// разблокировки Связки не должен съесть бюджет сети.
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			defer cancel()
			info, err := f.probe(ctx, r.URL, tok)
			if err != nil {
				r.State, r.Note = stateMissing, serverMessage(err)
				return
			}
			r.State, r.Count, r.Note = stateOK, info.Entries, "kbengine "+info.Version
		})
	}
	wg.Wait()
	return rows
}

// CheckServer is the «Проверить» button: the address passes the same rule as
// on save (http only to this Mac); the token is the one typed in the form or,
// if empty, the stored one — но только для сохраненного адреса этого id:
// набранный другой адрес токена не получит.
func (f *folderSettings) CheckServer(id, url, token string) (kbserver.Info, error) {
	if err := desktopconfig.CheckServerURL(url); err != nil {
		return kbserver.Info{}, humanErr{err.Error(), fmt.Errorf("%w: %w", desktopconfig.ErrSourceTarget, err)}
	}
	if token == "" && f.savedURL(id) == url {
		token = f.token(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	info, err := f.probe(ctx, url, token)
	if err != nil {
		return kbserver.Info{}, humanErr{serverMessage(err), err}
	}
	return info, nil
}

// savedURL returns the saved address of server id, "" if there is none.
func (f *folderSettings) savedURL(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.cfg.WithSources().Sources {
		if s.ID == id && s.Kind == desktopconfig.KindServer {
			return s.URL
		}
	}
	return ""
}

// keychainTokens is the production token store.
func keychainTokens() tokenStore { return keychain.Store{Service: "kb-desktop"} }

// probeServer asks a real kb server; таймаут задает контекст вызывающего.
func probeServer(ctx context.Context, url, token string) (kbserver.Info, error) {
	return kbserver.Client{URL: url, Token: token}.Info(ctx)
}
