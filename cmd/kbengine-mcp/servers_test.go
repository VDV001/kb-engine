package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
)

// memTokens — Связка ключей в памяти.
type memTokens map[string]string

func (m memTokens) Get(acct string) (string, error) {
	if v, ok := m[acct]; ok {
		return v, nil
	}
	return "", keychain.ErrNotFound
}

// Серверы берутся из конфига окна: включенные, в порядке списка, с токеном из
// Связки ключей по id источника. Выключенный не спрашивается, недоступный
// назван словами окна, а не пропущен.
func TestRemoteServers_SearchRemote(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"entries":[{"id":7,"title":"Агент"}],"total":3,"keys":["url a","url b"]}`))
	}))
	t.Cleanup(srv.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-a", Kind: desktopconfig.KindServer, Name: "vps", URL: srv.URL, Enabled: true},
		{ID: "server-off", Kind: desktopconfig.KindServer, URL: srv.URL + "/off", Enabled: false},
		{ID: "server-down", Kind: desktopconfig.KindServer, URL: downURL, Enabled: true},
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: dir, Enabled: true},
	}}
	if err := desktopconfig.Write(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	r := remoteServers{cfgPath: cfgPath, tokens: memTokens{"server-a": "t0ken-0123456789abcdef"}}
	got := r.SearchRemote(context.Background(), "агент")
	if len(got) != 2 || got[0].Source.ID != "server-a" || got[1].Source.ID != "server-down" {
		t.Fatalf("got = %+v", got)
	}
	if a := got[0]; a.Err != "" || len(a.Entries) != 1 || len(a.Keys) != 2 || a.Source.Name != "vps" {
		t.Fatalf("server-a = %+v", a)
	}
	if gotAuth != "Bearer t0ken-0123456789abcdef" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if got[1].Err == "" || len(got[1].Entries) != 0 {
		t.Fatalf("server-down = %+v", got[1])
	}
}

// Конфиг, который не читается, — не тихий пустой ответ: беда названа одной
// строкой источника, и агент видит, что серверы не спрошены.
func TestRemoteServers_ConfigUnreadable(t *testing.T) {
	r := remoteServers{cfgPath: filepath.Join(t.TempDir(), "нет.json"), tokens: memTokens{}}
	got := r.SearchRemote(context.Background(), "q")
	if len(got) != 1 || got[0].Err == "" {
		t.Fatalf("got = %+v", got)
	}
}

// syncTokens — Связка ключей в памяти, которую тест меняет во время поиска.
type syncTokens struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *syncTokens) Get(acct string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[acct]; ok {
		return v, nil
	}
	return "", keychain.ErrNotFound
}

func (s *syncTokens) set(acct, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[acct] = v
}

// Гонка токена: окно под замком конфига кладет новый токен в Связку, потом
// пишет новый адрес. Поиск агента в этот момент ждет замок и берет адрес и
// токен одной парой — новый токен не уходит старому хосту.
func TestRemoteServers_TokenNeverReachesOldHost(t *testing.T) {
	const oldTok, newTok = "old-t0ken-0123456789abcdef", "new-t0ken-0123456789abcdef"
	var mu sync.Mutex
	var oldAuth []string
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		oldAuth = append(oldAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"entries":[],"total":0,"keys":[]}`))
	}))
	t.Cleanup(oldSrv.Close)
	newSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":[{"id":1,"title":"Новый"}],"total":2,"keys":["k"]}`))
	}))
	t.Cleanup(newSrv.Close)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	write := func(url string) {
		t.Helper()
		cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
			{ID: "server-a", Kind: desktopconfig.KindServer, URL: url, Enabled: true},
		}}
		if err := desktopconfig.Write(cfgPath, cfg); err != nil {
			t.Fatal(err)
		}
	}
	write(oldSrv.URL)
	tokens := &syncTokens{m: map[string]string{"server-a": oldTok}}
	r := remoteServers{cfgPath: cfgPath, tokens: tokens}

	var got []mcpserver.RemoteFound
	done := make(chan struct{})
	err := filelock.With(cfgPath, func() error {
		tokens.set("server-a", newTok) // окно: токен раньше конфига
		go func() { got = r.SearchRemote(context.Background(), "q"); close(done) }()
		time.Sleep(100 * time.Millisecond) // без замка поиск успел бы прочесть старый адрес
		write(newSrv.URL)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	for _, a := range oldAuth {
		if strings.Contains(a, newTok) {
			t.Fatalf("новый токен ушел старому хосту: %v", oldAuth)
		}
	}
	if len(got) != 1 || got[0].Source.URL != newSrv.URL || len(got[0].Entries) != 1 {
		t.Fatalf("got = %+v", got)
	}
}

// Окно держит замок дольше срока агента (ждет разблокировки Связки): поиск
// не висит, а говорит, что серверы не спрошены.
func TestRemoteServers_ConfigLockBusy(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-a", Kind: desktopconfig.KindServer, URL: "https://192.0.2.1", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	r := remoteServers{cfgPath: cfgPath, tokens: memTokens{}}
	var got []mcpserver.RemoteFound
	_ = filelock.With(cfgPath, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		got = r.SearchRemote(ctx, "q")
		return nil
	})
	if len(got) != 1 || got[0].Source.ID != "config" || !strings.Contains(got[0].Err, "сохраняет") {
		t.Fatalf("got = %+v", got)
	}
}
