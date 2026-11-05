package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
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
		gotAuth = r.Header.Get("X-KB-Token")
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
	if gotAuth != "t0ken-0123456789abcdef" {
		t.Fatalf("X-KB-Token = %q", gotAuth)
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
	// Списка окна не знаем — свой каталог отвечает, как до списка источников.
	if len(got) != 2 || !got[0].Own || got[1].Source.ID != "config" || got[1].Err == "" {
		t.Fatalf("got = %+v", got)
	}
}

// Каталоги идут из списка окна, как серверы: свой (--catalog, по раскрытому
// пути) — строкой Own на своем месте, второй — найденными записями, выключенный
// не спрашивается.
func TestRemoteServers_CatalogsFollowWindowList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":[],"total":0,"keys":[]}`))
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(home, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ownPath := write("own.json", `{"entries":[]}`)
	write("work.json", `{"entries":[
		{"id":1,"title":"Агент в работе","category":"ai","status":"KEEP","date_added":"2026-01-01"},
		{"id":2,"title":"Кулинария","category":"misc","status":"KEEP","date_added":"2026-01-01"}]}`)
	cfgPath := filepath.Join(home, "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-a", Kind: desktopconfig.KindServer, URL: srv.URL, Enabled: true},
		{ID: "catalog-own", Kind: desktopconfig.KindCatalog, Name: "моя база", Path: "~/own.json", Enabled: true},
		{ID: "catalog-off", Kind: desktopconfig.KindCatalog, Path: "~/off.json", Enabled: false},
		{ID: "catalog-work", Kind: desktopconfig.KindCatalog, Path: "~/work.json", Enabled: true},
		{ID: "catalog-gone", Kind: desktopconfig.KindCatalog, Path: "~/gone.json", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	r := remoteServers{cfgPath: cfgPath, home: home, ownCatalog: ownPath, tokens: memTokens{}}
	got := r.SearchRemote(context.Background(), "агент")
	ids := []string{}
	for _, f := range got {
		ids = append(ids, f.Source.ID)
	}
	if strings.Join(ids, ",") != "server-a,catalog-own,catalog-work,catalog-gone" {
		t.Fatalf("ids = %v", ids)
	}
	if !got[1].Own || got[1].Source.Name != "моя база" || got[0].Own || got[2].Own {
		t.Fatalf("own = %+v", got)
	}
	if len(got[2].Catalog) != 1 || got[2].Catalog[0].ID() != 1 || got[2].Err != "" {
		t.Fatalf("work = %+v", got[2])
	}
	if got[3].Err == "" {
		t.Fatalf("недоступный каталог без беды: %+v", got[3])
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
		oldAuth = append(oldAuth, r.Header.Get("X-KB-Token"))
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
	// Свой каталог отвечает, серверы — нет, и сказано почему.
	if len(got) != 2 || !got[0].Own || got[1].Source.ID != "config" || !strings.Contains(got[1].Err, "сохраняет") {
		t.Fatalf("got = %+v", got)
	}
}

// slowTokens отвечает с задержкой — Связка, которую разблокируют.
type slowTokens struct {
	delay time.Duration
	tok   string
}

func (s slowTokens) Get(string) (string, error) { time.Sleep(s.delay); return s.tok, nil }

// Ожидание замка не съедает срок Связки: окно держало замок почти весь срок,
// а Связка отвечает не сразу — токен все равно прочитан, а не «разблокируйте».
func TestRemoteServers_LockWaitKeepsTokenBudget(t *testing.T) {
	const tok = "t0ken-0123456789abcdef"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-KB-Token")
		_, _ = w.Write([]byte(`{"entries":[],"total":0,"keys":[]}`))
	}))
	t.Cleanup(srv.Close)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-a", Kind: desktopconfig.KindServer, URL: srv.URL, Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	go func() {
		_ = filelock.With(cfgPath, func() error { close(held); time.Sleep(remoteTimeout - 400*time.Millisecond); return nil })
	}()
	<-held
	r := remoteServers{cfgPath: cfgPath, tokens: slowTokens{delay: 800 * time.Millisecond, tok: tok}}
	got := r.SearchRemote(context.Background(), "q")
	if len(got) != 1 || got[0].Err != "" || gotAuth != tok {
		t.Fatalf("got = %+v, X-KB-Token = %q", got, gotAuth)
	}
}

// Ревью #402. Свой каталог узнается и через символическую ссылку: окно
// хранит путь, каким его выбрал человек, MCP запущен с другим.
func TestRemoteServers_OwnCatalogThroughSymlink(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real.json")
	if err := os.WriteFile(real, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "link.json")); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "catalog-own", Kind: desktopconfig.KindCatalog, Path: "~/link.json", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	r := remoteServers{cfgPath: cfgPath, home: home, ownCatalog: real, tokens: memTokens{}}
	if got := r.SearchRemote(context.Background(), "агент"); len(got) != 1 || !got[0].Own {
		t.Fatalf("got = %+v", got)
	}
}

// Ревью #402. Второй каталог ищется так же, как его ищет окно (без словаря
// синонимов своего каталога): агент видит то, что человек. Пустой запрос
// серверы не спрашивает — им нечего искать.
func TestRemoteServers_SecondCatalogLikeWindow(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "work.json"), []byte(`{"entries":[
		{"id":1,"title":"Kubernetes","category":"ai","status":"KEEP","date_added":"2026-01-01"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "catalog-work", Kind: desktopconfig.KindCatalog, Path: "~/work.json", Enabled: true},
		{ID: "server-a", Kind: desktopconfig.KindServer, URL: "https://192.0.2.1", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	// Синоним «докер» → kubernetes лежит в словаре своего каталога; окно его
	// не знает, и вторые каталоги агент ищет без него (windowMatch).
	if windowMatch.Matches("Kubernetes", "докер") {
		t.Fatal("windowMatch знает синонимы своего каталога")
	}
	if !search.New(search.Dictionary{"докер": {Same: []string{"kubernetes"}}}).Matches("Kubernetes", "докер") {
		t.Fatal("контроль: словарь сам должен находить")
	}
	r := remoteServers{cfgPath: cfgPath, home: home, tokens: memTokens{}}
	if got := r.SearchRemote(context.Background(), "докер"); len(got) < 1 || len(got[0].Catalog) != 0 {
		t.Fatalf("второй каталог нашел по синониму своего: %+v", got)
	}
	got := r.SearchRemote(context.Background(), "")
	if len(got) != 1 || got[0].Source.ID != "catalog-work" || len(got[0].Catalog) != 1 {
		t.Fatalf("пустой запрос: %+v", got)
	}
}
