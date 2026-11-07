package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Откуда «Финансы» берут данные (решение владельца 07.11): первый kb-сервер,
// выбранный сервер из источников или свой kbengine serve на этом Mac.

func finServerSettings(t *testing.T, server string) *folderSettings {
	t.Helper()
	s, _, _ := newSourcesSettings(t)
	s.mu.Lock()
	s.cfg.Sources = append(s.cfg.Sources,
		src("server-a", desktopconfig.KindServer, "https://a.example.org", true),
		src("server-b", desktopconfig.KindServer, "https://b.example.org", false))
	s.cfg.Modules = []desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true, Server: server}}
	s.publishLocked(false)
	s.mu.Unlock()
	s.tokens.(*fakeTokens).m["server-a"] = "tok-a"
	s.tokens.(*fakeTokens).m["server-b"] = "tok-b"
	return s
}

func TestAppUpstream_followsTheChoice(t *testing.T) {
	for _, tc := range []struct {
		name, server    string
		ok              bool
		url, token, why string
	}{
		{"первый включенный", "", true, "https://a.example.org", "tok-a", ""},
		{"выбранный сервер", "server-a", true, "https://a.example.org", "tok-a", ""},
		{"выбранный выключен", "server-b", false, "", "", "выключен"},
		{"выбранного больше нет", "server-z", false, "", "", "нет в «Источниках»"},
		{"этот Mac, без токена", "http://127.0.0.1:8097", true, "http://127.0.0.1:8097", "", ""},
		// Правило «только этот Mac» — и там, где адрес используют: config.json
		// правят руками и пишут другие сборки (ревью #508).
		{"адрес в сети из файла", "https://evil.example.org", false, "", "", "только этого Mac"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up, ok := finServerSettings(t, tc.server).appUpstream()
			if ok != tc.ok || up.URL != tc.url || up.Token != tc.token || !strings.Contains(up.Why, tc.why) {
				t.Fatalf("upstream = %+v, ok %v", up, ok)
			}
		})
	}
}

// Выбор сохраняется, неподходящий — отказ ErrModuleServer, и переключение
// модуля (окно шлет только id и enabled) выбор не стирает.
func TestSetModuleServer(t *testing.T) {
	s := finServerSettings(t, "")
	if err := s.SetModuleServer(desktopconfig.ModuleFinances, "https://evil.example.org"); !errors.Is(err, desktopconfig.ErrModuleServer) {
		t.Fatalf("адрес в сети принят: %v", err)
	}
	if err := s.SetModuleServer(desktopconfig.ModuleFinances, "http://127.0.0.1:8097"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModules([]desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: false}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModuleServer(desktopconfig.ModuleFinances) != "http://127.0.0.1:8097" || cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Fatalf("в файле модули = %+v", cfg.Modules)
	}
	i := slices.IndexFunc(s.Modules(), func(m desktopconfig.Module) bool { return m.ID == desktopconfig.ModuleFinances })
	if i < 0 || s.Modules()[i].Server != "http://127.0.0.1:8097" {
		t.Fatalf("Modules() = %+v", s.Modules())
	}
}

// Свой serve без токена: пустой X-KB-Token не уходит вовсе.
func TestAppProxy_noTokenHeaderWithoutToken(t *testing.T) {
	var has bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, has = r.Header["X-Kb-Token"]
		_, _ = io.WriteString(w, `{}`)
	}))
	defer up.Close()
	rec := httptest.NewRecorder()
	proxyTo(t, up, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/api/finances", nil))
	if rec.Code != http.StatusOK || has {
		t.Fatalf("ответ %d, заголовок токена ушел: %v", rec.Code, has)
	}
}

// Выбранного сервера нет — экран говорит почему, а не «подключите сервер».
func TestAppProxy_saysWhyNoServer(t *testing.T) {
	p := newAppProxy(func() (appUpstream, bool) {
		return appUpstream{Why: "сервер «Яндекс» выключен"}, false
	}, appUI, nil)
	for _, path := range []string{"/app/", "/app/api/finances"} {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "«Яндекс» выключен") {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}
