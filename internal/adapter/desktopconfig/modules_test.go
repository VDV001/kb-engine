package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Плохая запись модулей не роняет чтение: модули — только вкладки, а конфиг
// читают и окно, и kbengine-mcp. Пустой id и повтор отбрасываются, первая
// запись остается; неизвестный id (более новая сборка) хранится.
func TestRead_BadModulesAreDropped(t *testing.T) {
	path := writeRaw(t, `{"sources":[{"id":"folder-1","kind":"folder","path":"/a","enabled":true}],
		"modules":[{"id":"chat","enabled":true},{"id":"","enabled":true},{"id":"finances","enabled":true},{"id":"finances","enabled":false}]}`)
	cfg, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatalf("err = %v, want nil: модули не должны ронять чтение источников", err)
	}
	want := []desktopconfig.Module{{ID: "chat", Enabled: true}, {ID: desktopconfig.ModuleFinances, Enabled: true}}
	if !slices.Equal(cfg.Modules, want) {
		t.Fatalf("modules = %+v, want %+v", cfg.Modules, want)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("sources = %+v", cfg.Sources)
	}
}

func TestConfig_ModuleOn(t *testing.T) {
	cfg := desktopconfig.Config{Modules: []desktopconfig.Module{
		{ID: desktopconfig.ModuleFinances, Enabled: true},
		{ID: "chat", Enabled: false},
	}}
	if !cfg.ModuleOn(desktopconfig.ModuleFinances) {
		t.Error("финансы включены, ModuleOn = false")
	}
	if cfg.ModuleOn("chat") {
		t.Error("выключенный модуль считается включенным")
	}
	// По умолчанию модулей нет: окно — только поиск, пока человек не включит.
	if (desktopconfig.Config{}).ModuleOn(desktopconfig.ModuleFinances) {
		t.Error("модуль без записи в конфиге включен")
	}
}

// Модули, и неизвестные тоже, переживают круг записи и чтения.
func TestWrite_ModulesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := desktopconfig.Config{Modules: []desktopconfig.Module{
		{ID: desktopconfig.ModuleFinances, Enabled: true},
		{ID: "chat", Enabled: false},
	}}
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Modules, cfg.Modules) {
		t.Fatalf("modules = %+v, want %+v", got.Modules, cfg.Modules)
	}
}

// Откуда «Финансы» берут данные (решение владельца 07.11: у кого-то сервер,
// у кого-то свой kbengine serve на этом Mac): пусто — первый включенный
// kb-сервер; id сервера из источников; адрес — только этого Mac, без токена.
func TestConfig_CheckModuleServer(t *testing.T) {
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: "/a", Enabled: true},
		{ID: "server-1", Kind: desktopconfig.KindServer, URL: "https://kb.example.org", Enabled: false},
	}}
	for _, tc := range []struct {
		name   string
		module desktopconfig.ModuleID
		server string
		ok     bool
	}{
		{"первый сервер", desktopconfig.ModuleFinances, "", true},
		{"сервер из источников, даже выключенный", desktopconfig.ModuleFinances, "server-1", true},
		{"этот Mac", desktopconfig.ModuleFinances, "http://127.0.0.1:8097", true},
		{"этот Mac по имени", desktopconfig.ModuleFinances, "http://localhost:9000", true},
		{"этот Mac по https", desktopconfig.ModuleFinances, "https://127.0.0.1:8443", true},
		{"адрес в сети — только источником с токеном", desktopconfig.ModuleFinances, "https://kb.example.org", false},
		{"http в сеть", desktopconfig.ModuleFinances, "http://10.0.0.5:8097", false},
		{"адрес с запросом", desktopconfig.ModuleFinances, "http://127.0.0.1:8097/?x=1", false},
		{"источник не сервер", desktopconfig.ModuleFinances, "folder-1", false},
		{"нет такого источника", desktopconfig.ModuleFinances, "server-9", false},
		{"модулю без данных сервер не выбирается", desktopconfig.ModuleChat, "server-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cfg.CheckModuleServer(tc.module, tc.server)
			if tc.ok && err != nil {
				t.Fatalf("отказ: %v", err)
			}
			if !tc.ok && !errors.Is(err, desktopconfig.ErrModuleServer) {
				t.Fatalf("err = %v, ждали ErrModuleServer", err)
			}
		})
	}
}

// Выбор сервера переживает запись и чтение; ModuleServer отдает его, модуль
// без записи — пусто (первый сервер, как было до выбора).
func TestConfig_ModuleServerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := desktopconfig.Config{Modules: []desktopconfig.Module{{ID: desktopconfig.ModuleFinances, Enabled: true, Server: "http://127.0.0.1:8097"}}}
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.ModuleServer(desktopconfig.ModuleFinances); s != "http://127.0.0.1:8097" {
		t.Fatalf("server = %q", s)
	}
	if s := (desktopconfig.Config{}).ModuleServer(desktopconfig.ModuleFinances); s != "" {
		t.Fatalf("без записи server = %q", s)
	}
}
