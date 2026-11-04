package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	getErr error  // сбой чтения, отличный от «записи нет» (связка заперта)
	touch  func() // зовется при каждой записи и удалении
}

func (f *fakeTokens) Set(acct, secret string) error {
	if f.fail != nil || acct == f.failOn {
		return errors.Join(f.fail, errors.New("keychain refused "+acct))
	}
	if f.touch != nil {
		f.touch()
	}
	f.m[acct] = secret
	return nil
}

func (f *fakeTokens) Get(acct string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if v, ok := f.m[acct]; ok {
		return v, nil
	}
	return "", keychain.ErrNotFound
}

func (f *fakeTokens) Delete(acct string) error {
	if f.touch != nil {
		f.touch()
	}
	delete(f.m, acct)
	return nil
}

// probeCall — что окно спросило у сервера.
type probeCall struct{ url, token string }

func newServerSettings(t *testing.T) (*folderSettings, *fakeTokens, *[]probeCall) {
	s, tok, calls, _ := newServerSettingsApplied(t)
	return s, tok, calls
}

// newServerSettingsApplied — то же, плюс счет вызовов apply: отказ обязан не
// трогать индекс.
func newServerSettingsApplied(t *testing.T) (*folderSettings, *fakeTokens, *[]probeCall, *appliedSources) {
	t.Helper()
	s, applied, _ := newSourcesSettings(t)
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
	return s, tok, &calls, applied
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
			s, tok, _, applied := newServerSettingsApplied(t)
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
			// Индекс не тронут: токены проверяются и пишутся до apply.
			if applied.calls != 0 {
				t.Fatalf("apply вызван %d раз при отказе", applied.calls)
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
	// Список отдается сразу, без сети: серверы — «проверяется».
	for _, r := range s.GetSources().Sources {
		if r.Kind == desktopconfig.KindServer && r.Enabled && r.State != stateChecking {
			t.Fatalf("GetSources ждал сеть: %+v", r)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("GetSources опросил серверы: %d", len(*calls))
	}
	rows := s.ServerStates()
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
	s, _, _ := newServerSettings(t)
	// Сохраненный токен берется только для сохраненного адреса — сервер
	// сначала сохраняется.
	if err := s.SetSources(serverList("server-ab12cd34", "https://kb.example.ru", true), nil, map[string]string{"server-ab12cd34": srvToken}); err != nil {
		t.Fatal(err)
	}
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

// Запись конфига отказала после записи токенов: токены возвращаются как было —
// Связка не расходится с конфигом, у нового id не остается сироты.
func TestSetSources_WriteFailureRollsBackTokens(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-00000001", "https://a.example.ru", true), nil, map[string]string{"server-00000001": "old-token-0123456789abcdefghijklmn"}); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "config.json") // каталог — это файл: запись падает
	list := append(serverList("server-00000001", "https://a.example.ru", true),
		desktopconfig.Source{ID: "server-00000002", Kind: desktopconfig.KindServer, URL: "https://b.example.ru", Enabled: true})
	if err := s.SetSources(list, nil, map[string]string{"server-00000001": srvToken, "server-00000002": srvToken}); err == nil {
		t.Fatal("ждали отказ записи")
	}
	if tok.m["server-00000001"] != "old-token-0123456789abcdefghijklmn" {
		t.Fatalf("старый токен не вернулся: %q", tok.m["server-00000001"])
	}
	if _, ok := tok.m["server-00000002"]; ok {
		t.Fatal("токен нового сервера остался сиротой")
	}
}

// Связка не прочиталась (заперта, отказ в доступе) — это не «токена нет»:
// сохранение отказывает и не трогает рабочий токен.
func TestSetSources_KeychainReadErrorKeepsToken(t *testing.T) {
	s, tok, _, applied := newServerSettingsApplied(t)
	tok.m["server-00000001"] = "old-token-0123456789abcdefghijklmn"
	tok.getErr = errors.New("keychain locked")
	tok.failOn = "server-00000001"
	if err := s.SetSources(serverList("server-00000001", "https://a.example.ru", true), nil, map[string]string{"server-00000001": srvToken}); err == nil {
		t.Fatal("ждали отказ")
	}
	tok.getErr = nil
	if tok.m["server-00000001"] != "old-token-0123456789abcdefghijklmn" || applied.calls != 0 {
		t.Fatalf("токен = %q, apply = %d", tok.m["server-00000001"], applied.calls)
	}
}

// Сменили адрес сервера и не ввели токен заново: старый токен удаляется, а не
// уходит новому хосту. С новым токеном — хранится новый.
func TestSetSources_URLChangeDropsToken(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("server-00000001", "https://kb.company.ru", true), nil, map[string]string{"server-00000001": srvToken}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(serverList("server-00000001", "https://kb.other.example", true), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := tok.m["server-00000001"]; ok {
		t.Fatal("токен остался после смены адреса")
	}
	if err := s.SetSources(serverList("server-00000001", "https://kb.third.example", true), nil, map[string]string{"server-00000001": "new-token-0123456789abcdefghijklmn"}); err != nil {
		t.Fatal(err)
	}
	if tok.m["server-00000001"] != "new-token-0123456789abcdefghijklmn" {
		t.Fatalf("новый токен = %q", tok.m["server-00000001"])
	}
}

// «Проверить» берет сохраненный токен только для сохраненного адреса: на
// набранный другой адрес токен не уходит.
func TestCheckServer_StoredTokenOnlyForSavedURL(t *testing.T) {
	s, _, calls := newServerSettings(t)
	if err := s.SetSources(serverList("server-00000001", "https://kb.company.ru", true), nil, map[string]string{"server-00000001": srvToken}); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	if _, err := s.CheckServer("server-00000001", "https://kb.other.example", ""); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].token != "" {
		t.Fatalf("токен ушел на другой адрес: %+v", *calls)
	}
}

// Одна карта «ошибка → текст»: «Проверить» и строка списка говорят о той же
// проблеме одинаково.
func TestCheckServer_MessageMatchesRow(t *testing.T) {
	s, _, _ := newServerSettings(t)
	_, err := s.CheckServer("server-new00000", "https://down.example.ru", srvToken)
	if err == nil || err.Error() != serverMessage(kbserver.ErrUnreachable) || !errors.Is(err, kbserver.ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
}

// Папки не открылись (apply отказал) после записи токенов: токены
// возвращаются как было — конфиг не пишется, Связка с ним не расходится.
func TestSetSources_ApplyFailureRollsBackTokens(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	tok.m["server-00000001"] = "old-token-0123456789abcdefghijklmn"
	s.apply = func([]string, []string, string) error { return errors.New("open root: permission denied") }
	if err := s.SetSources(serverList("server-00000001", "https://a.example.ru", true), nil, map[string]string{"server-00000001": srvToken}); err == nil {
		t.Fatal("ждали отказ apply")
	}
	if tok.m["server-00000001"] != "old-token-0123456789abcdefghijklmn" {
		t.Fatalf("токен не откатился: %q", tok.m["server-00000001"])
	}
}

// Связка не ответила (заперта, отказ в доступе) — это не «токена нет»:
// строка говорит о Связке, а не просит ввести токен заново.
func TestServerStates_KeychainError(t *testing.T) {
	s, tok, calls := newServerSettings(t)
	if err := s.SetSources(serverList("server-00000001", "https://kb.example.ru", true), nil, map[string]string{"server-00000001": srvToken}); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	tok.getErr = errors.New("keychain locked")
	rows := s.ServerStates()
	if len(rows) != 1 || rows[0].State != stateMissing || !strings.Contains(rows[0].Note, "Связка ключей") || !rows[0].HasToken {
		t.Fatalf("row = %+v", rows)
	}
	if len(*calls) != 0 {
		t.Fatal("сервер опрошен без токена при ошибке Связки")
	}
}

func TestServerMessage_Redirect(t *testing.T) {
	if got := serverMessage(kbserver.ErrRedirect); !strings.Contains(got, "перенаправ") {
		t.Fatalf("message = %q", got)
	}
}

// Отказ адреса в «Проверить» — словами человека, без внутреннего id.
func TestCheckServer_AddressMessageIsHuman(t *testing.T) {
	s, _, _ := newServerSettings(t)
	_, err := s.CheckServer("server-new00000", "http://192.168.1.10:8080", srvToken)
	if !errors.Is(err, desktopconfig.ErrSourceTarget) || strings.Contains(err.Error(), "check") || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
}
