package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Подключения хранятся в config.json без секретов: вид, подпись, чей вход,
// включено ли. Источник ссылается на подключение по id.
func TestConnections_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := desktopconfig.Load(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Connections = []desktopconfig.Connection{{ID: "google-1", Kind: desktopconfig.ConnGoogle, Name: "Google · мой", Account: "person@example.com", Enabled: true}}
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Load(path, dir)
	if err != nil || len(got.Connections) != 1 || got.Connections[0] != cfg.Connections[0] {
		t.Fatalf("connections = %+v, %v", got.Connections, err)
	}
	if c, ok := got.Connection("google-1"); !ok || c.Account != "person@example.com" {
		t.Fatalf("Connection = %+v, %v", c, ok)
	}
	if _, ok := got.Connection("nope"); ok {
		t.Fatal("нашлось несуществующее подключение")
	}
}

// Проверка: id есть и без повторов, подпись короткая; неизвестный вид — не
// ошибка (его записала более новая сборка), как у источников.
func TestConnections_Check(t *testing.T) {
	ok := []desktopconfig.Connection{{ID: "g1", Kind: desktopconfig.ConnGoogle, Enabled: true}, {ID: "x1", Kind: "yandex"}}
	if err := desktopconfig.CheckConnections(ok); err != nil {
		t.Fatalf("отвергнуто: %v", err)
	}
	for _, bad := range [][]desktopconfig.Connection{
		{{ID: "", Kind: desktopconfig.ConnGoogle}},
		{{ID: "g1", Kind: desktopconfig.ConnGoogle}, {ID: "g1", Kind: desktopconfig.ConnGoogle}},
		{{ID: "g1", Kind: desktopconfig.ConnGoogle, Name: string(make([]rune, 81))}},
	} {
		if err := desktopconfig.CheckConnections(bad); !errors.Is(err, desktopconfig.ErrConnections) {
			t.Errorf("принято %+v: %v", bad, err)
		}
	}
}

// Сервер распознавания речи — подключение с адресом (https или этот Мак) и
// моделью; ключ, если нужен, — в Связке.
func TestConnections_Speech(t *testing.T) {
	ok := []desktopconfig.Connection{{ID: "s1", Kind: desktopconfig.ConnSpeech, Name: "whisper.cpp", URL: "http://127.0.0.1:8080/v1", Model: "whisper-1", Enabled: true}}
	if err := desktopconfig.CheckConnections(ok); err != nil {
		t.Fatalf("отвергнуто: %v", err)
	}
	for _, u := range []string{"", "http://192.0.2.1:8080/v1", "ftp://x"} {
		bad := []desktopconfig.Connection{{ID: "s1", Kind: desktopconfig.ConnSpeech, URL: u}}
		if err := desktopconfig.CheckConnections(bad); !errors.Is(err, desktopconfig.ErrConnections) {
			t.Errorf("адрес %q принят: %v", u, err)
		}
	}
}
