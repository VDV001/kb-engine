package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
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
