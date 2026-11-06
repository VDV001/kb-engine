package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// portraitModel отвечает на чат, а на разбор портрета — JSON с фактами.
type portraitModel struct{ asked [][]chat.ModelMessage }

func (m *portraitModel) Complete(_ context.Context, msgs []chat.ModelMessage, _ []chat.Tool) (chat.ModelReply, error) {
	m.asked = append(m.asked, msgs)
	if strings.Contains(msgs[0].Content, "Ты ведешь портрет человека") {
		return chat.ModelReply{Content: `[{"section":"prefs","text":"Отвечать коротко"}]`}, nil
	}
	return chat.ModelReply{Content: "ответ"}, nil
}

func newPortraitDesk(t *testing.T) (*chatDesk, *portraitModel, chan []factView) {
	t.Helper()
	d, _, _, _ := newTestChatDesk(t)
	m := &portraitModel{}
	d.model = func(desktopconfig.Chat, string) chat.Model { return m }
	news := make(chan []factView, 4)
	d.memoryNew = func(f []factView) { news <- f }
	return d, m, news
}

// Портрет (#479): без папки — понятный отказ; в папке — правка, ответ
// видит одобренное, после ответа новое приходит на одобрение событием.
func TestChat_Portrait(t *testing.T) {
	d, m, news := newPortraitDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	if v, err := d.Portrait(); err != nil || v.Dir != "" {
		t.Fatalf("без папки: %+v %v", v, err)
	}
	if err := d.PortraitAdd("about", "Разработчик"); err == nil || !strings.Contains(err.Error(), "папк") {
		t.Fatalf("без папки добавлено: %v", err)
	}
	dir := t.TempDir()
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryDir: dir}, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.PortraitAdd("about", "Разработчик"); err != nil {
		t.Fatal(err)
	}
	rec, err := d.Ask(askReq{Question: "Как настроить голос?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.asked[0][0].Content, "Разработчик") || rec.Portrait == nil || rec.Portrait.Over {
		t.Fatalf("портрет в чате: %q, %+v", m.asked[0][0].Content, rec.Portrait)
	}
	select {
	case got := <-news:
		if len(got) != 1 || got[0].Text != "Отвечать коротко" || !got[0].Pending || got[0].Title != "Предпочтения" {
			t.Fatalf("новое = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("разбор после ответа не пришел")
	}
	v, _ := d.Portrait()
	if v.Dir != dir || len(v.Facts) != 2 || v.Budget != localPortraitBudget {
		t.Fatalf("портрет = %+v", v)
	}
	var pending string
	for _, f := range v.Facts {
		if f.Pending {
			pending = f.ID
		}
	}
	if err := d.PortraitApprove(pending, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Portrait(); v.Facts[1].Pending {
		t.Fatal("не одобрено")
	}
}

// Не помещается — без выбора человека портрет в чат не идет, ответ говорит
// об этом; «целиком» — идет весь.
func TestChat_PortraitOverBudget(t *testing.T) {
	d, m, _ := newPortraitDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryDir: t.TempDir(), MemoryOff: true}, ""); err != nil {
		t.Fatal(err)
	}
	for i, sec := range []string{"about", "work", "plans", "prefs", "misc", "work"} {
		if err := d.PortraitAdd(sec, strings.Repeat(string(rune('а'+i)), 290)); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := d.Ask(askReq{Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Portrait == nil || !rec.Portrait.Over || rec.Portrait.Size <= rec.Portrait.Budget || strings.Contains(m.asked[0][0].Content, "О человеке") {
		t.Fatalf("переполнение: %+v", rec.Portrait)
	}
	if _, err := d.Ask(askReq{Question: "q", Portrait: "full"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.asked[1][0].Content, "О человеке") {
		t.Fatal("«целиком» не дошло")
	}
	if len(m.asked) != 2 {
		t.Fatalf("разбор при MemoryOff: %d запросов", len(m.asked))
	}
}

// fakePortraitServer — /api/portrait kb-сервера: версия в ETag, запись с
// If-Match или If-None-Match: *; запоминает, с каким токеном писали.
func fakePortraitServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var mu sync.Mutex
	var data []byte
	var tokenSeen string
	ver := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		tokenSeen = r.Header.Get(kbserver.TokenHeader)
		if tokenSeen != srvToken || r.URL.Path != "/api/portrait" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		etag := fmt.Sprintf(`"v%d"`, ver)
		if r.Method == http.MethodGet {
			if data == nil {
				_, _ = w.Write([]byte(`{"facts":[]}`))
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = w.Write(data)
			return
		}
		if im := r.Header.Get("If-Match"); (data == nil && r.Header.Get("If-None-Match") != "*") || (data != nil && im != etag) {
			http.Error(w, "changed", http.StatusPreconditionFailed)
			return
		}
		data, _ = io.ReadAll(r.Body)
		ver++
		w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, ver))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &tokenSeen
}

// Портрет на kb-сервере (#479, решение владельца: хранение на выбор): окно
// пишет его тем же токеном, что у источника, и чат видит его так же, как из
// папки. Убранный из источников сервер — понятный отказ, а не пустой портрет.
func TestChat_PortraitOnServer(t *testing.T) {
	d, m, _ := newPortraitDesk(t)
	srv, seen := fakePortraitServer(t)
	const id = "server-ab12cd34"
	if err := d.settings.SetSources(serverList(id, srv.URL, true), nil, map[string]string{id: srvToken}); err != nil {
		t.Fatal(err)
	}
	c := desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryServer: id}
	if err := d.SetSettings(c, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.PortraitAdd("about", "Разработчик"); err != nil {
		t.Fatal(err)
	}
	if *seen != srvToken {
		t.Fatalf("токен на сервере = %q", *seen)
	}
	v, err := d.Portrait()
	if err != nil || v.Server != id || len(v.Facts) != 1 || v.Facts[0].Text != "Разработчик" {
		t.Fatalf("портрет с сервера: %+v %v", v, err)
	}
	if _, err := d.Ask(askReq{Question: "Как настроить голос?"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.asked[0][0].Content, "Разработчик") {
		t.Fatalf("в чате нет портрета с сервера: %q", m.asked[0][0].Content)
	}
	c.MemoryServer = "server-gone0000"
	if err := d.SetSettings(c, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.PortraitAdd("about", "Еще"); err == nil || !strings.Contains(err.Error(), "kb-сервер") {
		t.Fatalf("убранный сервер: %v", err)
	}
}
