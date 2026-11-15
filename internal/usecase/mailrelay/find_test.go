package mailrelay_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// finder is the window's search for the phone: выдача по запросу и текст по ref.
type finder struct {
	queries []string
	opened  []string
	fail    error
}

func (f *finder) Search(_ context.Context, q string) ([]mailrelay.Hit, error) {
	f.queries = append(f.queries, q)
	if f.fail != nil {
		return nil, f.fail
	}
	return []mailrelay.Hit{{Ref: "file:a.pdf", Kind: "pdf", Title: "Договор_Альфа.pdf", Snippet: "Поставщик обязуется", Where: "Договоры"}}, nil
}

func (f *finder) Open(_ context.Context, ref string) (mailrelay.Hit, error) {
	f.opened = append(f.opened, ref)
	if f.fail != nil {
		return mailrelay.Hit{}, f.fail
	}
	return mailrelay.Hit{Ref: ref, Kind: "pdf", Title: "Договор_Альфа.pdf", Text: strings.Repeat("я", 100)}, nil
}

// sourced answers with the places the answer came from — метки «взято».
type sourced struct{ asker }

func (s *sourced) AskSourced(ctx context.Context, chat, question string) (mailrelay.Answer, error) {
	c, text, err := s.Ask(ctx, chat, question)
	return mailrelay.Answer{Chat: c, Text: text, Sources: []mailrelay.Source{{Kind: "market", Name: "Ozon · Пример"}, {Kind: "file", Name: "отчет.xlsx"}}}, err
}

// payloads decodes what the desk posted, целиком.
func payloads(t *testing.T, b *box) []mailrelay.Payload {
	t.Helper()
	var out []mailrelay.Payload
	for _, m := range b.msgs {
		if m.From != domain.MailDesk {
			continue
		}
		var s fakeSealed
		var p mailrelay.Payload
		if json.Unmarshal(m.Sealed, &s) != nil || json.Unmarshal(s.Plain, &p) != nil {
			t.Fatalf("desk posted garbage: %s", m.Sealed)
		}
		out = append(out, p)
	}
	return out
}

// Поиск с телефона (решение владельца 15.11: чат и поиск сразу) идет в
// поисковик окна, не в модель; выдача уходит одним ответом в тот же чат ящика.
func TestPhoneSearch(t *testing.T) {
	b, a, f := &box{}, &asker{}, &finder{}
	b.phone("s1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindSearch, Text: "договор альфа"})
	r := newRelay(b, a, &store{})
	r.Finder = f
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a.asked) != 0 || len(f.queries) != 1 || f.queries[0] != "договор альфа" {
		t.Fatalf("модель %q, поиск %q", a.asked, f.queries)
	}
	got := payloads(t, b)
	if len(got) != 1 || got[0].Kind != mailrelay.KindResults || got[0].Text != "договор альфа" || len(got[0].Hits) != 1 || got[0].Hits[0].Ref != "file:a.pdf" {
		t.Fatalf("ответ %+v", got)
	}
}

// Текст найденного — по ref из выдачи, обрезан пределом ящика, как ответ модели.
func TestPhoneOpen(t *testing.T) {
	b, f := &box{}, &finder{}
	b.phone("s1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindOpen, Text: "file:a.pdf"})
	r := newRelay(b, &asker{}, &store{})
	r.Finder, r.MaxText = f, 60
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 1 || got[0].Kind != mailrelay.KindDoc || got[0].Title != "Договор_Альфа.pdf" || len(got[0].Text) > 60 || !strings.HasSuffix(got[0].Text, "в окне DVK") {
		t.Fatalf("ответ %+v", got)
	}
	if len(f.opened) != 1 || f.opened[0] != "file:a.pdf" {
		t.Fatalf("открыто %q", f.opened)
	}
}

// Окно без поисковика и отказ поисковика — ответ ошибкой, а не тишина.
func TestPhoneSearchRefused(t *testing.T) {
	for name, f := range map[string]*finder{"без поисковика": nil, "отказ": {fail: errors.New("источник не ответил")}} {
		t.Run(name, func(t *testing.T) {
			b := &box{}
			b.phone("s1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindSearch, Text: "x"})
			b.phone("s1", "p", 2, mailrelay.Payload{Kind: mailrelay.KindOpen, Text: "file:a.pdf"})
			r := newRelay(b, &asker{}, &store{})
			if f != nil {
				r.Finder = f
			}
			if err := r.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := payloads(t, b)
			if len(got) != 2 || got[0].Kind != mailrelay.KindError || got[1].Kind != mailrelay.KindError || got[0].Text == "" {
				t.Fatalf("ответы %+v", got)
			}
		})
	}
}

// Пустой запрос и пустой ref — не запрос: телефон так не шлет.
func TestPhoneSearchEmpty(t *testing.T) {
	b, f := &box{}, &finder{}
	b.phone("s1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindSearch, Text: "  "})
	b.phone("s1", "p", 2, mailrelay.Payload{Kind: mailrelay.KindOpen})
	r := newRelay(b, &asker{}, &store{})
	r.Finder = f
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.queries)+len(f.opened) != 0 || len(payloads(t, b)) != 0 {
		t.Fatalf("пустое ушло в поиск: %q %q", f.queries, f.opened)
	}
}

// Ответ модели несет, откуда он: метки «взято» на телефоне, как в окне.
func TestAnswerSources(t *testing.T) {
	b, a := &box{}, &sourced{}
	b.phone("c1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "заказы"})
	r := mailrelay.Relay{Box: b, Crypto: crypto{}, Asker: a, Store: &store{}}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 1 || got[0].Kind != mailrelay.KindAnswer || len(got[0].Sources) != 2 || got[0].Sources[0].Name != "Ozon · Пример" {
		t.Fatalf("ответ %+v", got)
	}
}
