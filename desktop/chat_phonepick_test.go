package main

import (
	"context"
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// pickDesk is a chat desk with one window model, one model allowed to the
// phone and two searched folders.
func pickDesk(t *testing.T) (*chatDesk, *echoModel, *desktopconfig.Chat, string) {
	t.Helper()
	d, m, _, used := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", PhoneModels: []string{"llama-phone"}}, ""); err != nil {
		t.Fatal(err)
	}
	d.settings.mu.Lock()
	d.settings.cfg.Sources = append(d.settings.cfg.Sources,
		desktopconfig.Source{ID: "src-a", Kind: desktopconfig.KindFolder, Path: t.TempDir(), Enabled: true},
		desktopconfig.Source{ID: "src-b", Kind: desktopconfig.KindFolder, Path: t.TempDir(), Enabled: true})
	d.settings.mu.Unlock()
	rec, err := d.Ask(askReq{Question: "сроки?"})
	if err != nil {
		t.Fatal(err)
	}
	return d, m, used, rec.ID
}

func phoneAsk(d *chatDesk, id string) (chatRecord, error) {
	return d.ask(context.Background(), askReq{ID: id, Question: "с телефона"}, askOut{worker: true})
}

// Модель с телефона — только разрешенная «Телефону: разрешить» (решение
// владельца 17.11), и проверка стоит там, где модель применяется: отозванное
// разрешение отказывает ходу до вызова модели.
func TestPhonePickModel(t *testing.T) {
	d, m, used, id := pickDesk(t)
	if err := d.SetPhonePick(id, "gpt-not-allowed", nil, true); !errors.Is(err, domain.ErrModelNotForPhone) {
		t.Fatalf("not allowed: %v", err)
	}
	if err := d.SetPhonePick(id, "llama-phone", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := phoneAsk(d, id); err != nil || used.Model != "llama-phone" {
		t.Fatalf("phone turn on %q, %v", used.Model, err)
	}
	if _, err := d.Ask(askReq{ID: id, Question: "из окна"}); err != nil || used.Model != "qwen3" {
		t.Errorf("window turn on %q, %v — ход окна идет по модели окна", used.Model, err)
	}
	c := d.config()
	c.PhoneModels = nil
	if err := d.SetSettings(c, ""); err != nil {
		t.Fatal(err)
	}
	before := m.n
	if _, err := phoneAsk(d, id); !errors.Is(err, domain.ErrModelNotForPhone) || m.n != before {
		t.Errorf("revoked: %v, model calls %d", err, m.n-before)
	}
}

// Источники с телефона только сужают выбор окна; не осталось разрешенных —
// отказ словами, а не молча «все» (решение владельца 17.11).
func TestPhonePickSources(t *testing.T) {
	d, m, _, id := pickDesk(t)
	if err := d.SetPhonePick(id, "", []string{"src-x"}, false); !errors.Is(err, domain.ErrNoChatSources) {
		t.Fatalf("unknown source: %v", err)
	}
	if err := d.SetPhonePick(id, "", []string{"src-b"}, false); err != nil {
		t.Fatal(err)
	}
	rec, err := phoneAsk(d, id)
	if err != nil || len(rec.PhoneOnly) != 1 || rec.PhoneOnly[0] != "src-b" {
		t.Fatalf("narrowed: %+v %v", rec.PhoneOnly, err)
	}
	d.settings.mu.Lock()
	for i := range d.settings.cfg.Sources {
		if d.settings.cfg.Sources[i].ID == "src-b" {
			d.settings.cfg.Sources[i].Enabled = false
		}
	}
	d.settings.mu.Unlock()
	before := m.n
	if _, err := phoneAsk(d, id); !errors.Is(err, domain.ErrNoChatSources) || m.n != before {
		t.Errorf("nothing left: %v, model calls %d", err, m.n-before)
	}
	if err := d.SetPhonePick(id, "", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := phoneAsk(d, id); err != nil {
		t.Errorf("back to all the window allows: %v", err)
	}
}

// Ящик видит выбор телефона через phoneChats, а телефон — модели окна и
// разрешенные телефону (#657).
func TestPhonePickWiring(t *testing.T) {
	d, _, _, id := pickDesk(t)
	pc := phoneChats{d: d, changed: func(string) {}}
	if err := pc.SetPick(context.Background(), id, mailrelay.Pick{Model: "llama-phone", Only: []string{"src-a"}}); err != nil {
		t.Fatal(err)
	}
	rec, err := d.load(id)
	if err != nil || rec.PhonePick != "llama-phone" || len(rec.PhoneOnly) != 1 {
		t.Fatalf("%+v %v", rec, err)
	}
	if err := pc.SetPick(context.Background(), id, mailrelay.Pick{}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := d.load(id); rec.PhonePick != "" || rec.PhoneOnly != nil {
		t.Errorf("empty pick — как в окне: %+v", rec)
	}
	st, err := phoneDesk{settings: d.settings}.State(context.Background())
	if err != nil || len(st.Models) != 2 || st.Models[0] != "qwen3" || st.Models[1] != "llama-phone" {
		t.Errorf("models %v %v", st.Models, err)
	}
}
