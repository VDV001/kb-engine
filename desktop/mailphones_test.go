package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

// newMailSettings: окно с kb-сервером srv1, у которого настоящий ящик
// (httpapi.MailHandler на blobfs, человек по токену — как serve --tenants).
func newMailSettings(t *testing.T) (*folderSettings, *fakeTokens, *httptest.Server) {
	t.Helper()
	store, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: store}, Phones: userauth.Phones{Store: store}, Visits: &userauth.Visits{Store: store}, Now: time.Now},
		func(string, ...any) {})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(kbserver.TokenHeader) != srvToken {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(httpapi.WithMailCaller(r.Context(), id, domain.MailDesk)))
	}))
	t.Cleanup(srv.Close)
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("srv1", srv.URL, true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	s.mailHTTP = srv.Client()
	// QR здесь — сам текст: тесты привязки не зависят от CoreImage (ревью #633);
	// настоящий QR проверяет qr_test.go.
	s.qr = func(text string) ([]byte, error) { return []byte(text), nil }
	return s, tok, srv
}

// pairURL reads the address the QR carries.
func pairURL(t *testing.T, p phonePairing) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.QR, "data:image/png;base64,"))
	if err != nil || !strings.HasPrefix(p.QR, "data:image/png;base64,") {
		t.Fatalf("qr is not a PNG data URI: %.40s, %v", p.QR, err)
	}
	return string(raw)
}

// Привязка: ключ чата рождается в окне и живет в Связке ключей; QR несет адрес
// страницы телефона, токен телефона и ключ — во фрагменте «#», который браузер
// на сервер не шлет. Вторая привязка — тот же ключ.
func TestMailPairPhone(t *testing.T) {
	s, tok, srv := newMailSettings(t)
	p, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := tok.m[mailKeyAccount("srv1")]
	if !ok {
		t.Fatal("no chat key in the keychain")
	}
	key, err := mailcrypt.ParseKey(stored)
	if err != nil {
		t.Fatal(err)
	}
	u := pairURL(t, p)
	prefix := srv.URL + "/m/#pair="
	phoneTok, keyText, ok := strings.Cut(strings.TrimPrefix(u, prefix), ".")
	if !strings.HasPrefix(u, prefix) || !ok || keyText != key.Encode() || phoneTok == "" || phoneTok == srvToken {
		t.Fatalf("qr carries %q", u)
	}
	list, err := s.MailPhones("srv1")
	if err != nil || len(list) != 1 || list[0].Device != p.Device || list[0].At == "" {
		t.Fatalf("phones: %+v, %v", list, err)
	}
	// Ответ окну: ни токена телефона, ни ключа открытым текстом — только картинка.
	if b, _ := json.Marshal(p); strings.Contains(string(b), phoneTok) || strings.Contains(string(b), keyText) {
		t.Errorf("pairing answer leaks a secret: %.120s", b)
	}
	p2, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	if _, k2, _ := strings.Cut(strings.TrimPrefix(pairURL(t, p2), prefix), "."); k2 != keyText {
		t.Error("second phone got another key: the first would not read it")
	}
}

// Отвязка: телефон больше не входит; последний отвязанный уносит ключ —
// следующая привязка начнется с нового (у отвязанного старый остался).
func TestMailRevokePhone(t *testing.T) {
	s, tok, _ := newMailSettings(t)
	a, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	first := tok.m[mailKeyAccount("srv1")]
	if list, err := s.MailPhones("srv1"); err != nil || len(list) != 2 || first == "" {
		t.Fatalf("two phones paired: %+v, %v, key %t", list, err, first != "")
	}
	if err := s.MailRevokePhone("srv1", a.Device); err != nil {
		t.Fatal(err)
	}
	if tok.m[mailKeyAccount("srv1")] != first {
		t.Fatal("key gone while a phone is still paired")
	}
	if err := s.MailRevokePhone("srv1", b.Device); err != nil {
		t.Fatal(err)
	}
	if _, ok := tok.m[mailKeyAccount("srv1")]; ok {
		t.Error("last phone unpaired, the key stays: the unpaired phone could read the next one")
	}
	if list, err := s.MailPhones("srv1"); err != nil || len(list) != 0 {
		t.Errorf("phones after revoke: %+v, %v", list, err)
	}
}

// Откат знает, что создал: сбой после новой записи в Связке или на сервере не
// оставляет ни ключа, ни токена телефона.
func TestMailPairPhoneRollsBack(t *testing.T) {
	t.Run("server refused", func(t *testing.T) {
		s, tok, _ := newMailSettings(t)
		tok.m["srv1"] = "wrong-token"
		if _, err := s.MailPairPhone("srv1"); err == nil {
			t.Fatal("paired with a wrong token")
		}
		if _, ok := tok.m[mailKeyAccount("srv1")]; ok {
			t.Error("new key left in the keychain after the server refused")
		}
	})
	t.Run("qr failed", func(t *testing.T) {
		s, _, _ := newMailSettings(t)
		s.qr = func(string) ([]byte, error) { return nil, errQRTooLong }
		if _, err := s.MailPairPhone("srv1"); !errors.Is(err, errQRTooLong) {
			t.Fatalf("pair: %v, want the QR error", err)
		}
		s.qr = nil
		if list, err := s.MailPhones("srv1"); err != nil || len(list) != 0 {
			t.Errorf("phone token left on the server: %+v, %v", list, err)
		}
	})
	t.Run("old key kept", func(t *testing.T) {
		s, tok, _ := newMailSettings(t)
		if _, err := s.MailPairPhone("srv1"); err != nil {
			t.Fatal(err)
		}
		key := tok.m[mailKeyAccount("srv1")]
		s.qr = func(string) ([]byte, error) { return nil, errQRTooLong }
		_, _ = s.MailPairPhone("srv1")
		if tok.m[mailKeyAccount("srv1")] != key {
			t.Error("rollback removed the key the first phone uses")
		}
	})
}

func TestMailPhonesUnknownServer(t *testing.T) {
	s, tok, _ := newMailSettings(t)
	before := len(tok.m)
	if _, err := s.MailPairPhone("nope"); err == nil {
		t.Error("paired a phone to a server that is not in the sources")
	}
	if _, err := s.MailPhones("nope"); err == nil {
		t.Error("listed phones of an unknown server")
	}
	if len(tok.m) != before {
		t.Error("keychain touched for an unknown server")
	}
}

// Телефонов на сервере нет — ключ всегда новый, как бы ни ушли прежние:
// отвязка, после которой список не прочитался, `users revoke`, другое окно,
// другой сервер под тем же id (ревью #633). Испорченный ключ при нуле телефонов
// тоже заменяется, а не запирает привязку.
func TestMailPairPhoneNewKeyWithoutPhones(t *testing.T) {
	s, tok, srv := newMailSettings(t)
	a, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	first := tok.m[mailKeyAccount("srv1")]
	c := kbserver.Client{URL: srv.URL, Token: srvToken, HTTP: srv.Client()}
	if err := c.RevokePhone(t.Context(), a.Device); err != nil { // мимо окна: ключ в Связке остался
		t.Fatal(err)
	}
	if _, err := s.MailPairPhone("srv1"); err != nil {
		t.Fatal(err)
	}
	if tok.m[mailKeyAccount("srv1")] == first {
		t.Error("no phones were left, yet the new phone got the old key")
	}
	for _, d := range mustPhones(t, s) {
		_ = c.RevokePhone(t.Context(), d)
	}
	tok.m[mailKeyAccount("srv1")] = "garbage"
	if _, err := s.MailPairPhone("srv1"); err != nil {
		t.Errorf("a broken key with no phones locks pairing: %v", err)
	}
}

func mustPhones(t *testing.T, s *folderSettings) []string {
	t.Helper()
	list, err := s.MailPhones("srv1")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range list {
		out = append(out, p.Device)
	}
	return out
}

// Источник убран из списка — его ключ чата уходит вместе с токеном.
func TestMailKeyDroppedWithTheSource(t *testing.T) {
	s, tok, _ := newMailSettings(t)
	if _, err := s.MailPairPhone("srv1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(withFolder("~/notes"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := tok.m[mailKeyAccount("srv1")]; ok {
		t.Error("the chat key outlived its source")
	}
}

// QR не рисуется на этой машине — отказ до ключа и до телефона на сервере.
func TestMailPairPhoneWithoutQR(t *testing.T) {
	s, tok, _ := newMailSettings(t)
	s.qr = nil
	was := qrAvailable
	qrAvailable = func() bool { return false }
	t.Cleanup(func() { qrAvailable = was })
	if _, err := s.MailPairPhone("srv1"); err == nil {
		t.Fatal("paired without a QR")
	}
	if _, ok := tok.m[mailKeyAccount("srv1")]; ok {
		t.Error("key created for a QR that cannot be drawn")
	}
	if list := mustPhones(t, s); len(list) != 0 {
		t.Errorf("phone created on the server: %v", list)
	}
}

// В списке телефонов окно показывает имя по браузеру и последний вход: так
// видно, что телефон вошел по QR, и какой это телефон (владелец 16.11).
func TestMailPhonesNameAndSeen(t *testing.T) {
	seen := time.Date(2026, 11, 16, 9, 6, 0, 0, time.UTC)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"devices":[{"device":"p-1","at":"2026-11-16T09:04:00Z","seen":"2026-11-16T09:06:00Z","name":"Samsung SM-S918B","seen_known":true},{"device":"p-2","at":"2026-11-16T09:05:00Z","seen_known":true},{"device":"p-3","at":"2026-11-16T09:05:00Z"}]}`))
	}))
	t.Cleanup(srv.Close)
	s, _, _ := newServerSettings(t)
	if err := s.SetSources(serverList("srv1", srv.URL, true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	s.mailHTTP = srv.Client()
	list, err := s.MailPhones("srv1")
	if err != nil || len(list) != 3 {
		t.Fatalf("phones: %+v, %v", list, err)
	}
	if list[0].Name != "Samsung SM-S918B" || list[0].Seen != seen.Local().Format("02.01.2006 15:04") {
		t.Errorf("visited phone row %+v", list[0])
	}
	if list[1].Name != "" || list[1].Seen != "" || list[1].SeenUnknown {
		t.Errorf("phone that never came in %+v", list[1])
	}
	// Сервер не прочитал входы или старше #652 (нет seen_known): окно не ждет
	// молча входа, которого не увидит (ревью #652).
	if !list[2].SeenUnknown {
		t.Errorf("phone with unknown visits %+v", list[2])
	}
}

// «Переименовать» в окне: имя видно в списке, плохое имя — понятный отказ (#622).
func TestMailRenamePhone(t *testing.T) {
	s, _, _ := newMailSettings(t)
	a, err := s.MailPairPhone("srv1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MailRenamePhone("srv1", a.Device, "iPhone 17 Pro"); err != nil {
		t.Fatal(err)
	}
	if list, err := s.MailPhones("srv1"); err != nil || len(list) != 1 || list[0].Name != "iPhone 17 Pro" {
		t.Fatalf("after rename: %+v, %v", list, err)
	}
	err = s.MailRenamePhone("srv1", a.Device, strings.Repeat("я", 41))
	if err == nil || !strings.Contains(err.Error(), "до 40") {
		t.Fatalf("long name: %v", err)
	}
}
