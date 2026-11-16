package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/mail"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

const phoneToken = "phone-token-622-test"

// newWorkerSettings: окно с kb-сервером srv1, у которого настоящий ящик;
// srvToken — сторона компьютера, phoneToken — телефона (как serve --tenants).
func newWorkerSettings(t *testing.T) (*folderSettings, *fakeTokens, *httptest.Server, *atomic.Int64) {
	t.Helper()
	return newWorkerSettingsSeeing(t, func(string) {})
}

// newWorkerSettingsSeeing is newWorkerSettings that reports each desk request's path.
func newWorkerSettingsSeeing(t *testing.T, seen func(path string)) (*folderSettings, *fakeTokens, *httptest.Server, *atomic.Int64) {
	t.Helper()
	store, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: store}, Phones: userauth.Phones{Store: store}, Files: mailbox.Files{Blob: store, Del: store}, Now: time.Now},
		func(string, ...any) {})
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		side := domain.MailDesk
		switch r.Header.Get(kbserver.TokenHeader) {
		case srvToken:
			hits.Add(1)
			seen(r.URL.Path)
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
	phoneSends(t, srv, key, chat, counter, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: text})
}

// phoneSends posts any request of the phone.
func phoneSends(t *testing.T, srv *httptest.Server, key mailcrypt.Key, chat string, counter uint64, p mailrelay.Payload) {
	t.Helper()
	plain, _ := json.Marshal(p)
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
	if len(calls) != 1 || calls[0] != (askCall{"", "привет"}) || len(got) != 1 || got[0].Kind != mailrelay.KindAnswer || got[0].Text != "ответ на привет" {
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

// Ответ ушел в ящик — окно зовет телефоны push (#622).
func TestMailWorkerNotifiesAfterTheAnswer(t *testing.T) {
	var notifies int
	s, tok, srv, _ := newWorkerSettingsSeeing(t, func(p string) {
		if p == "/api/mail/notify" {
			notifies++
		}
	})
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	ask := func(_ context.Context, _, q string) (string, string, error) {
		return "w-1", "ответ на " + q, nil
	}
	phoneAsks(t, srv, key, "c-1", 1, "привет")
	if err := newMailWorker(s, ask, t.TempDir()).tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if notifies != 1 {
		t.Fatalf("notify calls %d, want 1", notifies)
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

// Вопрос с телефона виден в окне сразу (#646): страница слышит начало хода с
// чатом окна и вопросом, шаги и конец — и при отказе модели тоже.
func TestMailWorkerShowsThePhoneTurn(t *testing.T) {
	w := newMailWorker(nil, nil, t.TempDir())
	var got []phoneTurn
	w.turn = func(e phoneTurn) { got = append(got, e) }
	fail := error(nil)
	w.answer = func(_ context.Context, _, _ string, _ []mailrelay.File, began func(string), step func(chat.Use)) (mailrelay.Answer, error) {
		began("w-1")
		step(chat.Use{Kind: chat.UseDocs, What: "поставки"})
		return mailrelay.Answer{Chat: "w-1", Text: "12"}, fail
	}
	var steps []string
	if _, err := (workerAsker{w}).AskStepped(t.Context(), "", "сколько?", func(s mailrelay.Step) { steps = append(steps, s.Text) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (phoneTurn{Chat: "w-1", Question: "сколько?"}) || got[1].Step == nil || got[1].Step.What != "поставки" || got[2] != (phoneTurn{Chat: "w-1", Done: true}) {
		t.Fatalf("page heard %+v", got)
	}
	if len(steps) != 1 || steps[0] != "Документы: поставки" {
		t.Errorf("phone steps %v", steps)
	}
	got, fail = nil, errors.New("model down")
	_, _ = (workerAsker{w}).AskStepped(t.Context(), "", "еще?", nil)
	if len(got) == 0 || got[len(got)-1] != (phoneTurn{Chat: "w-1", Done: true}) {
		t.Fatalf("after a failed turn the page heard %+v — «отвечает» would hang", got)
	}
}

// Факты из ответа ждут круга работника; второй раз их не отдают (#647).
func TestMailWorkerQueuesFacts(t *testing.T) {
	w := newMailWorker(nil, nil, t.TempDir())
	w.remember(portraitNews{Chat: "w-1", Facts: []factView{{ID: "f1", Title: "Работа", Text: "Ведет закупки"}}})
	w.remember(portraitNews{Chat: "w-2"})
	got := w.Take()
	if len(got) != 1 || got[0].Chat != "w-1" || got[0].Payload.Kind != mailrelay.KindRemember ||
		len(got[0].Payload.Facts) != 1 || got[0].Payload.Facts[0] != (mailrelay.Fact{ID: "f1", Title: "Работа", Text: "Ведет закупки"}) {
		t.Fatalf("notes %+v", got)
	}
	if again := w.Take(); len(again) != 0 {
		t.Errorf("notes given twice: %+v", again)
	}
	// Не больше maxPhoneFacts за раз: одна пачка не раздувает сообщение ящика (ревью #655).
	many := make([]factView, 15)
	for i := range many {
		many[i] = factView{ID: fmt.Sprintf("f%d", i), Text: "x"}
	}
	w.remember(portraitNews{Chat: "w-1", Facts: many})
	if got := w.Take(); len(got) != 1 || len(got[0].Payload.Facts) != maxPhoneFacts {
		t.Errorf("15 facts → %+v", got)
	}
	// «Новое — только в портрете» окна действует и на телефон (ревью #655).
	w.quiet = func() bool { return true }
	w.remember(portraitNews{Chat: "w-1", Facts: many[:1]})
	if got := w.Take(); len(got) != 0 {
		t.Errorf("memory quiet, the phone still gets %+v", got)
	}
}

// Решение телефона — портретом окна через Decide (только ждущий одобрения
// факт); странице — снять карточку; сбой — карточка остается (#647, ревью #655).
func TestPhoneMemoryDecides(t *testing.T) {
	var calls []string
	fail := error(nil)
	m := phoneMemory{
		decide: func(ctx context.Context, id string, yes bool, text string) error {
			if ctx == nil {
				t.Error("decide without the worker's ctx")
			}
			calls = append(calls, fmt.Sprintf("decide %s %t %s", id, yes, text))
			return fail
		},
		decided: func(id string, yes bool) { calls = append(calls, fmt.Sprintf("decided %s %t", id, yes)) },
	}
	if err := m.Decide(t.Context(), "f1", true, "Ведет закупки"); err != nil {
		t.Fatal(err)
	}
	want := "decide f1 true Ведет закупки|decided f1 true"
	if strings.Join(calls, "|") != want {
		t.Fatalf("calls %v", calls)
	}
	calls, fail = nil, errors.New("портрет недоступен")
	if err := m.Decide(t.Context(), "f3", false, ""); err == nil || len(calls) != 1 {
		t.Fatalf("failed decide: %v, %v — the page must not drop its card", err, calls)
	}
}

// --- модель и источники чата на телефоне (#647): показ, менять — в окне

// Окно отвечает телефону на desk настоящим кругом работника: состояние окна
// подключено там же, где ход модели (useChat), а не полем, поставленным в тесте.
func TestMailWorkerAnswersTheDesk(t *testing.T) {
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	w := newMailWorker(s, chatAsker(d), t.TempDir())
	w.useChat(d)
	phoneSends(t, srv, key, "desk-0101", 1, mailrelay.Payload{Kind: mailrelay.KindDesk, Text: "state"})
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := phoneReads(t, srv, key)
	if len(got) != 1 || got[0].Kind != mailrelay.KindDesk || got[0].Desk == nil || got[0].Desk.Model != "qwen3" {
		t.Fatalf("phone got %+v — the desk is not wired into the worker's relay", got)
	}
}

// Состояние окна для телефона: модель настроек, включенные источники и
// подключения, разрешенные чату; закрытое чату телефон не увидит.
func TestPhoneDeskState(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if err := s.SetSources(serverList("srv1", "https://kb.example.ru", true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cfg.Chat = desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "qwen3", Mode: desktopconfig.ChatModeTools}
	s.cfg.Connections = append(s.cfg.Connections,
		desktopconfig.Connection{ID: "oz1", Kind: desktopconfig.ConnOzon, Name: "Пример", Enabled: true, Share: true},
		desktopconfig.Connection{ID: "oz2", Kind: desktopconfig.ConnOzon, Name: "Закрытый", Enabled: true})
	// Вид, записанный более новой сборкой, хранится и не читается: чат в нем не ищет (ревью #656).
	s.cfg.Sources = append(s.cfg.Sources, desktopconfig.Source{ID: "x1", Kind: "future", Path: "/x", Enabled: true})
	s.mu.Unlock()
	names := func() string {
		st, err := phoneDesk{settings: s}.State(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range st.Sources {
			out = append(out, fmt.Sprintf("%s|%t", x.Name, x.Conn))
		}
		return st.Model + " " + strings.Join(out, ",")
	}
	if got := names(); got != "qwen3 notes|false,kb.example.ru|false,Ozon · Пример|true" {
		t.Errorf("desk %q — закрытое чату подключение и нечитаемый источник телефону не показываются", got)
	}
	// Режим «окно ищет»: подключения чат не спрашивает — телефон их не называет (ревью #656).
	s.mu.Lock()
	s.cfg.Chat.Mode = desktopconfig.ChatModeContext
	s.mu.Unlock()
	if got := names(); got != "qwen3 notes|false,kb.example.ru|false" {
		t.Errorf("context mode: %q", got)
	}
}

// Ход с телефона не стирает источники, выбранные для чата в окне, и говорит
// телефону модель и источники чата.
func TestPhoneTurnKeepsTheChatSources(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.settings.SetSources(serverList("srv1", "https://kb.example.ru", true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	rec, err := d.Ask(askReq{Question: "сроки?", Sources: []string{"srv1"}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := chatAnswerer(d)(t.Context(), rec.ID, "еще?", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := d.load(rec.ID)
	if strings.Join(got.Sources, ",") != "srv1" {
		t.Errorf("chat sources %v after a phone turn, want srv1", got.Sources)
	}
	if strings.Join(a.Only, ",") != "srv1" {
		t.Errorf("the phone is told sources %v", a.Only)
	}
}

// Голос телефона настоящим кругом (#648): запись лежит в файлах ящика,
// сообщение называет ее хешем; окно забирает, расшифровывает ключом чата,
// распознает (Apple Speech — kb-listen --file) и удаляет.
func TestMailWorkerHearsThePhone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple Speech — только на Маке")
	}
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	phone := kbserver.Client{URL: srv.URL, Token: phoneToken, HTTP: srv.Client()}
	sealed, err := mailcrypt.SealFile(key, []byte("RIFF-wav"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := phone.MailFilePut(t.Context(), sealed)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(sealed)
	phoneSends(t, srv, key, "c-1", 1, mailrelay.Payload{Kind: mailrelay.KindVoice, Text: mailrelay.VoiceDictate,
		File: &mailrelay.FileRef{ID: id, SHA256: hex.EncodeToString(sum[:]), Size: len(sealed)}})
	w := newMailWorker(s, func(context.Context, string, string) (string, string, error) { return "w", "ok", nil }, t.TempDir())
	w.useVoice(&dictation{tmpDir: t.TempDir(), bin: fakeListen(t, `prev=""; f=""
for a; do [ "$prev" = "--file" ] && f="$a"; prev="$a"; done
[ "$(cat "$f")" = "RIFF-wav" ] && echo '{"text":"срок поставки","final":true}' || echo '{"error":"не та запись"}'
`)})
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := phoneReads(t, srv, key)
	if len(got) != 1 || got[0].Kind != mailrelay.KindHeard || got[0].Text != "срок поставки" {
		t.Fatalf("phone got %+v", got)
	}
	if _, err := phone.MailFileGet(t.Context(), id); !errors.Is(err, kbserver.ErrNoSuchMailFile) {
		t.Errorf("the recording stays on the server: %v", err)
	}
}

// Телефон знает, кто распознает его голос (ревью безопасности #660): звук
// уходит дальше компьютера всюду, кроме Apple Speech без сети.
func TestPhoneDeskSaysWhoHears(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple Speech — только на Маке")
	}
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	w := newMailWorker(d.settings, chatAsker(d), t.TempDir())
	w.useChat(d)
	hear := func() string {
		st, err := w.desk.State(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return st.Hear
	}
	if got := hear(); got != "" {
		t.Errorf("before voice is wired: %q", got)
	}
	check := func(onDevice bool) string {
		return fakeListen(t, fmt.Sprintf(`echo '{"auth":"authorized","onDevice":%t,"available":true}'`, onDevice))
	}
	w.useVoice(&dictation{bin: check(true)})
	if got := hear(); got != mailrelay.HearDevice {
		t.Errorf("on the device: %q", got)
	}
	w.useVoice(&dictation{bin: check(false)})
	if got := hear(); got != mailrelay.HearApple {
		t.Errorf("through Apple: %q", got)
	}
	d.settings.mu.Lock()
	d.settings.cfg.Chat.Listen = "cloud:mini"
	d.settings.mu.Unlock()
	if got := hear(); got != mailrelay.HearCloud {
		t.Errorf("cloud: %q", got)
	}
}

// Окно, упавшее посреди распознавания, оставило расшифрованную запись во
// временной папке: при старте голоса она убирается (ревью безопасности #660).
func TestUseVoiceSweepsRecordings(t *testing.T) {
	tmp := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, n := range []string{"kb-phone-1.wav", "kb-listen-2.wav", "other.wav"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(tmp, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	newMailWorker(nil, nil, t.TempDir()).useVoice(&dictation{tmpDir: tmp})
	left, _ := os.ReadDir(tmp)
	if len(left) != 1 || left[0].Name() != "other.wav" {
		t.Errorf("left %v", left)
	}
}

// Файл с телефона доходит до модели настоящим кругом (#650): куски в файлах
// ящика, окно собирает, расшифровывает ключом чата и прикладывает к вопросу.
func TestMailWorkerAttachesThePhoneFile(t *testing.T) {
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	phone := kbserver.Client{URL: srv.URL, Token: phoneToken, HTTP: srv.Client()}
	var pieces []mailrelay.FileRef
	text := "Срок поставки по договору — 45 дней."
	for _, part := range []string{text[:20], text[20:]} {
		sealed, err := mailcrypt.SealFile(key, []byte(part))
		if err != nil {
			t.Fatal(err)
		}
		id, err := phone.MailFilePut(t.Context(), sealed)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(sealed)
		pieces = append(pieces, mailrelay.FileRef{ID: id, SHA256: hex.EncodeToString(sum[:]), Size: len(sealed)})
	}
	phoneSends(t, srv, key, "c-1", 1, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "какой срок?",
		Attach: &mailrelay.Attachment{Name: "договор.txt", Type: "text/plain", Size: len(text), Pieces: pieces}})
	w := newMailWorker(s, chatAsker(d), t.TempDir())
	w.useChat(d)
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := phoneReads(t, srv, key)
	if len(got) == 0 || got[len(got)-1].Kind != mailrelay.KindAnswer {
		t.Fatalf("phone got %+v", got)
	}
	var seen bool
	for _, turn := range m.asked {
		for _, msg := range turn {
			seen = seen || strings.Contains(msg.Content, "45 дней")
		}
	}
	if !seen {
		t.Error("the model never saw the attached file")
	}
}

// Окно называет телефону слушателя, только когда распознавание сработает
// (повторное ревью #660): выключенное подключение и запрет Apple Speech —
// голоса нет, а не «распознает Ваш сервер» с отказом на каждую запись.
func TestPhoneDeskHearsOnlyWhatWorks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple Speech — только на Маке")
	}
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	w := newMailWorker(d.settings, chatAsker(d), t.TempDir())
	w.useChat(d)
	hear := func() string {
		st, err := w.desk.State(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return st.Hear
	}
	w.useVoice(&dictation{bin: fakeListen(t, `echo '{"auth":"denied","onDevice":true,"available":true}'`)})
	if got := hear(); got != "" {
		t.Errorf("Apple Speech denied: %q", got)
	}
	d.settings.mu.Lock()
	d.settings.cfg.Chat.Listen = "speech-off"
	d.settings.cfg.Connections = append(d.settings.cfg.Connections, desktopconfig.Connection{ID: "speech-off", Kind: desktopconfig.ConnSpeech, Name: "whisper", Enabled: false})
	d.settings.mu.Unlock()
	if got := hear(); got != "" {
		t.Errorf("disabled speech server: %q", got)
	}
}

// Уборка при старте трогает только старые записи: свежая — запись другого
// окна (отладочная сборка рядом), ее удаление сломало бы чужую диктовку.
func TestUseVoiceKeepsFreshRecordings(t *testing.T) {
	tmp := t.TempDir()
	fresh := filepath.Join(tmp, "kb-listen-live.wav")
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	newMailWorker(nil, nil, t.TempDir()).useVoice(&dictation{tmpDir: tmp})
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh recording was swept: %v", err)
	}
}

// Один чат в двух местах (#649), настоящим кругом: переименование и удаление
// с телефона доходят до файла чата окна, правка в окне — до телефона.
func TestMailWorkerEditsChatsBothWays(t *testing.T) {
	s, tok, srv, _ := newWorkerSettings(t)
	key := mailcrypt.NewKey()
	tok.m[mailKeyAccount("srv1")] = key.Encode()
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	w := newMailWorker(s, chatAsker(d), t.TempDir())
	w.useChat(d)
	phoneAsks(t, srv, key, "c-1", 1, "сроки?")
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	list, err := d.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("window chats %v, %v", list, err)
	}
	id := list[0].ID
	if err := d.RenameChat(id, "Поставки в окне"); err != nil {
		t.Fatal(err)
	}
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := phoneReads(t, srv, key)
	if last := got[len(got)-1]; last.Kind != mailrelay.KindRename || last.Text != "Поставки в окне" {
		t.Fatalf("the phone got %+v — the window's rename did not reach it", last)
	}
	phoneSends(t, srv, key, "c-1", 2, mailrelay.Payload{Kind: mailrelay.KindRename, Text: "Поставки"})
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec, err := d.load(id); err != nil || rec.Title != "Поставки" {
		t.Fatalf("window title %q, %v", rec.Title, err)
	}
	phoneSends(t, srv, key, "c-1", 3, mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"})
	if err := w.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.load(id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the window chat survived the phone's delete: %v", err)
	}
}
