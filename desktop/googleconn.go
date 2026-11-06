package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/google"
)

// Подключение Google (решение владельца 06.11: «дать доступ — удобнее»):
// вход один раз, дальше закрытые документы читаются по ссылке. Окно просит
// только чтение Диска. Клиент OAuth — свой у человека (Desktop app в Google
// Cloud): в config.json — вид, подпись и чей вход, секреты — в Связке ключей.

// errNoGoogle — ссылка Google, а включенного подключения нет.
var errNoGoogle = errors.New("нет подключения Google")

// connectTimeout — сколько ждать согласия в браузере.
const connectTimeout = 5 * time.Minute

func clientAccount(id string) string  { return "conn:" + id + ":client" }
func refreshAccount(id string) string { return "conn:" + id + ":refresh" }

type oauthClient struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

type googleAccess struct {
	at  string
	exp time.Time
}

// googleDesk holds the Google connections of the window.
type googleDesk struct {
	settings *folderSettings
	client   func(id, secret string) google.Client
	open     func(url string) // браузер со страницей согласия
	timeout  time.Duration

	mu     sync.Mutex
	access map[string]googleAccess
}

func newGoogleDesk(s *folderSettings) *googleDesk {
	return &googleDesk{settings: s, timeout: connectTimeout, access: map[string]googleAccess{},
		client: func(id, secret string) google.Client { return google.Client{ClientID: id, ClientSecret: secret} }}
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand на macOS не отказывает
	return base64.RawURLEncoding.EncodeToString(b)
}

// Connect signs in: страница согласия в браузере, ответ на 127.0.0.1, обмен
// кода на токены, подключение в настройки.
func (g *googleDesk) Connect(clientID, secret string) (connRow, error) {
	clientID, secret = strings.TrimSpace(clientID), strings.TrimSpace(secret)
	if !strings.HasSuffix(clientID, ".apps.googleusercontent.com") || secret == "" {
		return connRow{}, errors.New("нужны Client ID (…apps.googleusercontent.com) и Client secret клиента OAuth «Desktop app»")
	}
	c := g.client(clientID, secret)
	verifier := randomString()
	code, redirect, err := g.consent(c, verifier)
	if err != nil {
		return connRow{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := c.Exchange(ctx, code, redirect, verifier)
	if err != nil {
		return connRow{}, fmt.Errorf("вход не принят Google — проверьте Client ID и Client secret: %w", err)
	}
	if tok.Refresh == "" {
		return connRow{}, errors.New("постоянного входа (refresh-токена) Google не выдал: удалите DVK в доступах аккаунта Google и подключите заново")
	}
	email, err := c.Account(ctx, tok.Access)
	if err != nil {
		return connRow{}, googleErr(err)
	}
	conn := desktopconfig.Connection{ID: "google-" + randomString()[:8], Kind: desktopconfig.ConnGoogle, Name: "Google · " + email, Account: email, Enabled: true}
	if err := g.save(conn, oauthClient{ID: clientID, Secret: secret}, tok.Refresh); err != nil {
		return connRow{}, err
	}
	g.mu.Lock()
	g.access[conn.ID] = googleAccess{at: tok.Access, exp: time.Now().Add(50 * time.Minute)}
	g.mu.Unlock()
	return g.settings.googleConn(conn), nil
}

// consent opens the consent page and waits for its answer on 127.0.0.1;
// state отсекает чужой ответ.
func (g *googleDesk) consent(c google.Client, verifier string) (code, redirect string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	redirect = fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	state := randomString()
	type answer struct {
		code string
		err  error
	}
	got := make(chan answer, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			// Чужой ответ (другая вкладка, локальный процесс) не обрывает вход:
			// ждем настоящий (ревью #466).
			http.Error(w, "не этот вход", http.StatusBadRequest)
			return
		}
		a := consentAnswer(q, state)
		msg := "Готово: DVK подключен к Google Диску. Окно можно закрыть."
		if a.err != nil {
			msg = "Не подключено: " + a.err.Error()
		}
		_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>DVK</title><p style=\"font:16px system-ui;margin:40px\">%s</p>", html.EscapeString(msg))
		select {
		case got <- answer(a):
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	g.open(c.AuthURL(redirect, state, verifier))
	select {
	case a := <-got:
		return a.code, redirect, a.err
	case <-time.After(g.timeout):
		return "", "", errors.New("согласие в браузере не пришло за 5 минут")
	}
}

// consentAnswer reads the redirect from the consent page.
func consentAnswer(q url.Values, state string) struct {
	code string
	err  error
} {
	a := struct {
		code string
		err  error
	}{code: q.Get("code")}
	switch {
	case q.Get("state") != state:
		a.err = errors.New("ответ Google не к этому входу — попробуйте еще раз")
	case q.Get("error") != "":
		a.err = errors.New("на странице Google — отказ в доступе")
	case a.code == "":
		a.err = errors.New("код от Google не пришел")
	}
	return a
}

// save stores the secrets in the Keychain and the connection in the
// settings; сбой на любом шаге откатывает записанное.
func (g *googleDesk) save(conn desktopconfig.Connection, cl oauthClient, refresh string) error {
	raw, _ := json.Marshal(cl)
	if err := g.settings.tokens.Set(clientAccount(conn.ID), string(raw)); err != nil {
		return fmt.Errorf("клиент не сохранен в Связке ключей: %w", err)
	}
	if err := g.settings.tokens.Set(refreshAccount(conn.ID), refresh); err != nil {
		_ = g.settings.tokens.Delete(clientAccount(conn.ID))
		return fmt.Errorf("вход не сохранен в Связке ключей: %w", err)
	}
	if err := g.settings.updateConfig(func(cfg *desktopconfig.Config) { cfg.Connections = append(cfg.Connections, conn) }); err != nil {
		_ = g.settings.tokens.Delete(clientAccount(conn.ID))
		_ = g.settings.tokens.Delete(refreshAccount(conn.ID))
		return err
	}
	return nil
}

// SetEnabled turns a connection on or off: выключенное не читает ничего.
func (g *googleDesk) SetEnabled(id string, on bool) error {
	return g.settings.updateConfig(func(cfg *desktopconfig.Config) {
		for i := range cfg.Connections {
			if cfg.Connections[i].ID == id {
				cfg.Connections[i].Enabled = on
			}
		}
	})
}

// Remove deletes a connection and its secrets.
func (g *googleDesk) Remove(id string) error {
	if err := g.settings.updateConfig(func(cfg *desktopconfig.Config) {
		cfg.Connections = slices.DeleteFunc(cfg.Connections, func(c desktopconfig.Connection) bool { return c.ID == id })
		if cfg.Chat.Listen == id {
			cfg.Chat.Listen = "" // удаленный сервер распознавания — назад к Apple Speech (ревью #468)
		}
	}); err != nil {
		return err
	}
	g.mu.Lock()
	delete(g.access, id)
	g.mu.Unlock()
	_ = g.settings.tokens.Delete(refreshAccount(id))
	_ = g.settings.tokens.Delete(clientAccount(id))
	_ = g.settings.tokens.Delete(speechKeyAccount(id))
	return nil
}

// Read reads a Google link through the first enabled Google connection.
//
// ponytail: берется первое включенное подключение Google. Потолок: два
// аккаунта, и документ открыт только второму. Путь: пробовать по очереди на
// ErrNoAccess.
func (g *googleDesk) Read(ctx context.Context, link string) (google.Doc, error) {
	id := google.FileID(link)
	if id == "" {
		return google.Doc{}, errors.New("это не ссылка на документ Google")
	}
	cfg := g.settings.snapshot().cfg
	i := slices.IndexFunc(cfg.Connections, func(c desktopconfig.Connection) bool { return c.Kind == desktopconfig.ConnGoogle && c.Enabled })
	if i < 0 {
		return google.Doc{}, errNoGoogle
	}
	conn := cfg.Connections[i]
	c, at, err := g.token(ctx, conn.ID)
	if err != nil {
		return google.Doc{}, err
	}
	d, err := c.Read(ctx, at, id)
	if err != nil {
		return google.Doc{}, fmt.Errorf("%s: %w", conn.Name, googleErr(err))
	}
	return d, nil
}

// token returns a client and an access token for connection id: из памяти
// или обновлением по refresh-токену из Связки.
func (g *googleDesk) token(ctx context.Context, id string) (google.Client, string, error) {
	raw, err := g.settings.tokens.Get(clientAccount(id))
	if err != nil {
		return google.Client{}, "", fmt.Errorf("клиент Google не прочитан из Связки ключей: %w", err)
	}
	var cl oauthClient
	if err := json.Unmarshal([]byte(raw), &cl); err != nil {
		return google.Client{}, "", fmt.Errorf("клиент Google в Связке ключей испорчен — подключите заново: %w", err)
	}
	c := g.client(cl.ID, cl.Secret)
	g.mu.Lock()
	t, ok := g.access[id]
	g.mu.Unlock()
	if ok && time.Now().Before(t.exp) {
		return c, t.at, nil
	}
	refresh, err := g.settings.tokens.Get(refreshAccount(id))
	if err != nil {
		return google.Client{}, "", fmt.Errorf("вход Google не прочитан из Связки ключей: %w", err)
	}
	at, err := c.Refresh(ctx, refresh)
	if err != nil {
		return google.Client{}, "", googleErr(err)
	}
	g.mu.Lock()
	g.access[id] = googleAccess{at: at, exp: time.Now().Add(50 * time.Minute)}
	g.mu.Unlock()
	return c, at, nil
}

// googleErr names a Google refusal in words.
func googleErr(err error) error {
	switch {
	case errors.Is(err, google.ErrReconnect):
		return errors.New("вход Google истек или отозван — подключите заново в «Подключениях»")
	case errors.Is(err, google.ErrNoAccess):
		return errors.New("у подключенного аккаунта нет доступа к документу")
	case errors.Is(err, google.ErrNotFound):
		return errors.New("документа нет: проверьте ссылку")
	case errors.Is(err, google.ErrTooBig):
		return errors.New("документ больше 20 МБ")
	}
	return fmt.Errorf("нет ответа от Google: %w", err)
}

// updateConfig changes the settings under the same locks as the other saves.
func (f *folderSettings) updateConfig(change func(*desktopconfig.Config)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Connections = slices.Clone(f.cfg.Connections)
	change(&next)
	if err := desktopconfig.CheckConnections(next.Connections); err != nil {
		return err
	}
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false)
		return nil
	})
}
