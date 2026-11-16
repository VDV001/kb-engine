package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	h := httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: store}, Phones: userauth.Phones{Store: store}, Now: time.Now},
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
	w.answer = func(_ context.Context, _, _ string, _ mailrelay.Options, began func(string), step func(chat.Use)) (mailrelay.Answer, error) {
		began("w-1")
		step(chat.Use{Kind: chat.UseDocs, What: "поставки"})
		return mailrelay.Answer{Chat: "w-1", Text: "12"}, fail
	}
	var steps []string
	if _, err := (workerAsker{w}).AskStepped(t.Context(), "", "сколько?", mailrelay.Options{}, func(s mailrelay.Step) { steps = append(steps, s.Text) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (phoneTurn{Chat: "w-1", Question: "сколько?"}) || got[1].Step == nil || got[1].Step.What != "поставки" || got[2] != (phoneTurn{Chat: "w-1", Done: true}) {
		t.Fatalf("page heard %+v", got)
	}
	if len(steps) != 1 || steps[0] != "Документы: поставки" {
		t.Errorf("phone steps %v", steps)
	}
	got, fail = nil, errors.New("model down")
	_, _ = (workerAsker{w}).AskStepped(t.Context(), "", "еще?", mailrelay.Options{}, nil)
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

// Выбор телефона — модель и источники — доходит до хода окна (#647).
func TestMailWorkerPassesThePhoneChoice(t *testing.T) {
	w := newMailWorker(nil, nil, t.TempDir())
	var got mailrelay.Options
	w.answer = func(_ context.Context, _, _ string, opts mailrelay.Options, _ func(string), _ func(chat.Use)) (mailrelay.Answer, error) {
		got = opts
		return mailrelay.Answer{Chat: "w-1", Text: "ок"}, nil
	}
	want := mailrelay.Options{Model: "qwen3", Only: []string{"f-1"}}
	if _, err := (workerAsker{w}).AskStepped(t.Context(), "", "сроки?", want, nil); err != nil {
		t.Fatal(err)
	}
	if got.Model != "qwen3" || strings.Join(got.Only, ",") != "f-1" {
		t.Fatalf("answer got %+v", got)
	}
}

// Состояние окна для поля ввода телефона: модель и список, источники и
// подключения, разрешенные чату (#647).
func TestPhoneDeskState(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if err := s.SetSources(serverList("srv1", "https://kb.example.ru", true), nil, map[string]string{"srv1": srvToken}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cfg.Connections = append(s.cfg.Connections,
		desktopconfig.Connection{ID: "oz1", Kind: desktopconfig.ConnOzon, Name: "Пример", Enabled: true, Share: true},
		desktopconfig.Connection{ID: "oz2", Kind: desktopconfig.ConnOzon, Name: "Закрытый", Enabled: true})
	s.mu.Unlock()
	d := phoneDesk{settings: s, models: func() (modelsView, error) {
		return modelsView{Current: "qwen3", Recent: []modelChoice{{ID: "qwen3"}, {ID: "llama3"}}}, nil
	}}
	st, err := d.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != "qwen3" || strings.Join(st.Models, ",") != "qwen3,llama3" {
		t.Errorf("models %+v", st)
	}
	var names []string
	for _, x := range st.Sources {
		names = append(names, fmt.Sprintf("%s|%t", x.Name, x.Conn))
	}
	if strings.Join(names, ",") != "notes|false,kb.example.ru|false,Ozon · Пример|true" {
		t.Errorf("sources %v — закрытое для чата подключение телефону не показывается", names)
	}
}
