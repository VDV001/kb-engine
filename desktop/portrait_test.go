package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
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
