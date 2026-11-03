package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

const srvToken = "k3Jx9_secret-token-0123456789abcdefXYZ"

// fakeTokens — Связка ключей в памяти: тесты проверяют, что окно кладет и
// убирает, а не саму Связку (у нее свои тесты на временной связке).
type fakeTokens struct {
	m      map[string]string
	fail   error
	failOn string // отказ только для этой учетки
}

func (f *fakeTokens) Set(acct, secret string) error {
	if f.fail != nil || acct == f.failOn {
		return errors.Join(f.fail, errors.New("keychain refused "+acct))
	}
	f.m[acct] = secret
	return nil
}

func (f *fakeTokens) Get(acct string) (string, error) {
	if v, ok := f.m[acct]; ok {
		return v, nil
	}
	return "", keychain.ErrNotFound
}

func (f *fakeTokens) Delete(acct string) error { delete(f.m, acct); return nil }

// probeCall — что окно спросило у сервера.
type probeCall struct{ url, token string }

func newServerSettings(t *testing.T) (*folderSettings, *fakeTokens, *[]probeCall) {
	t.Helper()
	s, _, _ := newSourcesSettings(t)
	tok := &fakeTokens{m: map[string]string{}}
	var calls []probeCall
	var mu sync.Mutex // опросы серверов идут параллельно
	s.tokens = tok
	s.probe = func(_ context.Context, url, token string) (kbserver.Info, error) {
		mu.Lock()
		calls = append(calls, probeCall{url, token})
		mu.Unlock()
		switch {
		case strings.Contains(url, "down"):
			return kbserver.Info{}, kbserver.ErrUnreachable
		case token != srvToken:
			return kbserver.Info{}, kbserver.ErrUnauthorized
		}
		return kbserver.Info{Version: "0.29.0", Entries: 2431}, nil
	}
	return s, tok, &calls
}

func serverList(id, url string, on bool) []desktopconfig.Source {
	return append(withFolder("~/notes"), desktopconfig.Source{ID: id, Kind: desktopconfig.KindServer, URL: url, Enabled: on})
}

// Токен уходит в Связку ключей до записи конфига, по id источника; в конфиг
// не попадает.
func TestSetSources_StoresServerToken(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken}); err != nil {
		t.Fatal(err)
	}
	if tok.m["server-ab12cd34"] != srvToken {
		t.Fatalf("Связка = %v", tok.m)
	}
	raw, _ := os.ReadFile(s.path)
	if strings.Contains(string(raw), srvToken) {
		t.Fatal("токен попал в config.json")
	}
}

func TestSetSources_TokenRefusals(t *testing.T) {
	tests := []struct {
		name   string
		tokens map[string]string
		fail   error
	}{
		// Токен для id, которого нет среди серверов списка, — ошибка окна, а
		// не повод положить в Связку чужую запись.
		{"чужой id", map[string]string{"server-ffff0000": srvToken}, nil},
		{"токен для папки", map[string]string{"folder-1": srvToken}, nil},
		{"Связка отказала", map[string]string{"server-ab12cd34": srvToken}, errors.New("keychain locked")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, tok, _ := newServerSettings(t)
			tok.fail = tt.fail
			before, _ := os.ReadFile(s.path)
			list := serverList("server-ab12cd34", "https://kb.example.ru", true)
			list[0].ID = "folder-1"
			if err := s.SetSources(list, nil, tt.tokens); err == nil {
				t.Fatal("ждали отказ")
			}
			if after, _ := os.ReadFile(s.path); string(before) != string(after) {
				t.Fatal("конфиг записан при отказе")
			}
			if len(tok.m) != 0 {
				t.Fatalf("в Связке осталось %v", tok.m)
			}
		})
	}
}

// Убранный сервер забирает свой токен из Связки ключей.
func TestSetSources_RemovedServerDropsToken(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(withFolder("~/notes"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := tok.m["server-ab12cd34"]; ok {
		t.Fatal("токен убранного сервера остался в Связке")
	}
}

// Экран показывает живое состояние сервера: версию и число записей, «нужен
// токен», «нет связи». Выключенный сервер не опрашивается.
func TestGetSources_ServerStates(t *testing.T) {
	s, _, calls := newServerSettings(t)
	list := append(withFolder("~/notes"),
		desktopconfig.Source{ID: "server-00000001", Kind: desktopconfig.KindServer, URL: "https://kb.example.ru", Enabled: true},
		desktopconfig.Source{ID: "server-00000002", Kind: desktopconfig.KindServer, URL: "https://no-token.example.ru", Enabled: true},
		desktopconfig.Source{ID: "server-00000003", Kind: desktopconfig.KindServer, URL: "https://down.example.ru", Enabled: true},
		desktopconfig.Source{ID: "server-00000004", Kind: desktopconfig.KindServer, URL: "https://off.example.ru", Enabled: false},
	)
	if err := s.SetSources(list, nil, map[string]string{"server-00000001": srvToken, "server-00000003": srvToken}); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	rows := s.GetSources().Sources
	got := map[string]sourceRow{}
	for _, r := range rows {
		got[r.ID] = r
	}
	if r := got["server-00000001"]; r.State != stateOK || r.Count != 2431 || !strings.Contains(r.Note, "0.29.0") || !r.HasToken {
		t.Errorf("рабочий = %+v", r)
	}
	if r := got["server-00000002"]; r.State != stateMissing || !strings.Contains(r.Note, "токен") || r.HasToken {
		t.Errorf("без токена = %+v", r)
	}
	if r := got["server-00000003"]; r.State != stateMissing || !strings.Contains(r.Note, "связи") {
		t.Errorf("недоступный = %+v", r)
	}
	if r := got["server-00000004"]; r.State != stateOff {
		t.Errorf("выключенный = %+v", r)
	}
	for _, c := range *calls {
		if strings.Contains(c.url, "off.") {
			t.Fatal("выключенный сервер опрошен")
		}
	}
}

// «Проверить» до сохранения: новый токен из формы, а без него — сохраненный
// в Связке (правка адреса без повторного ввода токена).
func TestCheckServer(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	tok.m["server-ab12cd34"] = srvToken
	if info, err := s.CheckServer("server-new00000", "https://kb.example.ru", srvToken); err != nil || info.Entries != 2431 {
		t.Fatalf("новый токен: %+v %v", info, err)
	}
	if _, err := s.CheckServer("server-ab12cd34", "https://kb.example.ru", ""); err != nil {
		t.Fatalf("сохраненный токен: %v", err)
	}
	if _, err := s.CheckServer("server-new00000", "https://kb.example.ru", ""); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Fatalf("без токена: %v", err)
	}
	// Адрес проверяется тем же правилом, что и при сохранении: http в сеть —
	// отказ до запроса, токен не уходит открытым текстом.
	if _, err := s.CheckServer("server-new00000", "http://kb.example.ru", srvToken); !errors.Is(err, desktopconfig.ErrSourceTarget) {
		t.Fatalf("http в сеть: %v", err)
	}
}

// Связка отказала на втором токене: первый, уже записанный, возвращается к
// прежнему значению — конфиг не пишется, и токены не должны разойтись с ним.
func TestSetSources_TokenFailureRollsBack(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	list := append(withFolder("~/notes"),
		desktopconfig.Source{ID: "server-00000001", Kind: desktopconfig.KindServer, URL: "https://a.example.ru", Enabled: true},
		desktopconfig.Source{ID: "server-00000002", Kind: desktopconfig.KindServer, URL: "https://b.example.ru", Enabled: true},
	)
	tok.m["server-00000001"] = "old-token"
	tok.failOn = "server-00000002"
	// Порядок обхода map случаен: прогон повторяется, чтобы первым хоть раз
	// записался server-00000001.
	for range 20 {
		if err := s.SetSources(list, nil, map[string]string{"server-00000001": srvToken, "server-00000002": srvToken}); err == nil {
			t.Fatal("ждали отказ")
		}
		if tok.m["server-00000001"] != "old-token" {
			t.Fatalf("первый токен не откатился: %q", tok.m["server-00000001"])
		}
		if _, ok := tok.m["server-00000002"]; ok {
			t.Fatal("второй токен остался")
		}
	}
}
