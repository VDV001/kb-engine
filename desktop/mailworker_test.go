package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

const phoneToken = "phone-token-622-test"

// newWorkerSettings: окно с kb-сервером srv1, у которого настоящий ящик;
// srvToken — сторона компьютера, phoneToken — телефона (как serve --tenants).
func newWorkerSettings(t *testing.T) (*folderSettings, *fakeTokens, *httptest.Server, *atomic.Int64) {
	t.Helper()
	store, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: store}, Phones: userauth.Phones{Store: store}, Now: time.Now},
		func(string, ...any) {})
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		side := domain.MailDesk
		switch r.Header.Get(kbserver.TokenHeader) {
		case srvToken:
			hits.Add(1)
		case phoneToken:
			side = domain.MailPhone
		default:
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(httpapi.WithMailCaller(r.Context(), id, side)))
	}))
	t.Cleanup(srv.Close)
	s, tok, _ := newServerSettings(t)
	if err := s.SetSources(serverList("srv1", srv.URL, true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	s.mailHTTP = srv.Client()
	return s, tok, srv, &hits
}

var phoneSender = mailcrypt.Sender{1, 2, 3, 4, 5, 6, 7, 8}

func phoneAsks(t *testing.T, srv *httptest.Server, key mailcrypt.Key, chat string, counter uint64, text string) {
	t.Helper()
	plain, _ := json.Marshal(mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: text})
	sealed, err := mailcrypt.Seal(key, mailcrypt.Meta{Chat: chat, From: domain.MailPhone, Sender: phoneSender, Counter: counter}, plain)
	if err != nil {
		t.Fatal(err)
	}
	c := kbserver.Client{URL: srv.URL, Token: phoneToken, HTTP: srv.Client()}
	if _, err := c.MailPost(t.Context(), chat, sealed); err != nil {
		t.Fatal(err)
	}
}

// phoneReads opens what the desk answered.
func phoneReads(t *testing.T, srv *httptest.Server, key mailcrypt.Key) []mailrelay.Payload {
	t.Helper()
	c := kbserver.Client{URL: srv.URL, Token: phoneToken, HTTP: srv.Client()}
	p, err := c.MailSince(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []mailrelay.Payload
	for _, m := range p.Messages {
		if m.From != string(domain.MailDesk) {
			continue
		}
		meta, plain, err := mailcrypt.Open(key, m.Chat, domain.MailDesk, m.Sealed)
		if err != nil {
			t.Fatalf("phone cannot open the desk's answer: %v", err)
		}
		if meta.Sender == phoneSender {
			t.Fatal("desk sealed under the phone's sender")
		}
		var pl mailrelay.Payload
		if err := json.Unmarshal(plain, &pl); err != nil {
			t.Fatal(err)
		}
		out = append(out, pl)
	}
	return out
}

type askCall struct{ chat, question string }

// Телефон спросил — окно ответило в тот же чат ящика ключом из Связки; после
// перезапуска окна вопрос не задается второй раз (состояние в файле).
func TestMailWorkerAnswersThePhone(t *testing.T) {
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	var calls []askCall
	ask := func(_ context.Context, chat, q string) (string, string, error) {
		calls = append(calls, askCall{chat, q})
		return "w-1", "ответ на " + q, nil
	}
	dir := t.TempDir()
	phoneAsks(t, srv, key, "c-1", 1, "привет")
	if err := newMailWorker(s, ask, dir).tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := phoneReads(t, srv, key)
	if len(calls) != 1 || calls[0] != (askCall{"", "привет"}) || len(got) != 1 || got[0] != (mailrelay.Payload{Kind: mailrelay.KindAnswer, Text: "ответ на привет"}) {
		t.Fatalf("calls %+v, phone got %+v", calls, got)
	}
	phoneAsks(t, srv, key, "c-1", 2, "еще")
	if err := newMailWorker(s, ask, dir).tick(t.Context()); err != nil { // «перезапуск»
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] != (askCall{"w-1", "еще"}) {
		t.Errorf("after restart: %+v — the old question asked again or the chat lost", calls)
	}
}

// Сервер без ключа чата (телефоны не привязаны) окно не опрашивает вовсе.
func TestMailWorkerSkipsServersWithoutPhones(t *testing.T) {
	s, _, _, hits := newWorkerSettings(t)
	ask := func(context.Context, string, string) (string, string, error) {
		t.Error("asked without a key")
		return "", "", nil
	}
	if err := newMailWorker(s, ask, t.TempDir()).tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("polled a server without phones: %d requests", n)
	}
}

// --- ревью #635

// Ход с телефона не стирает черновик «нового чата», который человек набирает в окне.
func TestMailWorkerKeepsTheDraft(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveDraft("", "набираю в окне"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chatAsker(d)(t.Context(), "", "с телефона"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Draft(""); got != "набираю в окне" {
		t.Errorf("draft after a phone turn: %q", got)
	}
}

// Связка ключей — не каждые 10 с: ключ и токен читаются раз, заново — после
// привязки или отвязки телефона в этом окне.
func TestMailWorkerReadsTheKeychainRarely(t *testing.T) {
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	tok.gets = map[string]int{}
	w := newMailWorker(s, func(context.Context, string, string) (string, string, error) { return "w", "ok", nil }, t.TempDir())
	for range 3 {
		if err := w.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n, m := tok.gets[mailKeyAccount("srv1")], tok.gets["srv1"]; n > 1 || m > 1 {
		t.Errorf("3 ticks read the keychain: key %d, token %d times", n, m)
	}
	s.qr = func(text string) ([]byte, error) { return []byte(text), nil }
	before := tok.gets[mailKeyAccount("srv1")]
	if _, err := s.MailPairPhone("srv1"); err != nil {
		t.Fatal(err)
	}
	_ = w.tick(t.Context())
	if tok.gets[mailKeyAccount("srv1")] <= before { // без телефонов привязка ключ не читает, а заводит новый
		t.Error("tick after pairing did not re-read the key")
	}
	_ = srv
}

// Отказ сервера насовсем (неверный чат, велико) — mailrelay.ErrRefused: такой
// ответ уходит из исходящих, а не затыкает ящик.
func TestMailBoxMarksRefusals(t *testing.T) {
	_, _, srv, _ := newWorkerSettings(t)
	b := mailBox{kbserver.Client{URL: srv.URL, Token: srvToken, HTTP: srv.Client()}}
	if _, err := b.Post(t.Context(), "Bad Chat", []byte("x")); !errors.Is(err, mailrelay.ErrRefused) {
		t.Errorf("bad chat: %v, want ErrRefused", err)
	}
	if _, err := b.Post(t.Context(), "c-1", make([]byte, domain.MaxMailSealed+1)); !errors.Is(err, mailrelay.ErrRefused) {
		t.Errorf("too large: %v, want ErrRefused", err)
	}
}
