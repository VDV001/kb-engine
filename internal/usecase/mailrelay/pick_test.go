package mailrelay_test

import (
	"context"
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// picker records the phone's choices; refuse — отказ окна словами.
type picker struct {
	set    []string
	refuse error
}

func (p *picker) SetPick(_ context.Context, chat string, pk mailrelay.Pick) error {
	if p.refuse != nil {
		return p.refuse
	}
	p.set = append(p.set, chat+"="+pk.Model)
	return nil
}

// Выбор модели и источников с телефона (#657): применяется до хода, отказ окна
// — словами и без хода; подтвержденный выбор возвращается в ответе.
func TestRelayAppliesThePhonesPick(t *testing.T) {
	b, s := &box{}, &store{}
	a := &asker{}
	b.phone("c-1", "A", 1, q("привет"))
	r := newRelay(b, a, s)
	pk := &picker{}
	r.Picker = pk
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	pick := &mailrelay.Pick{Model: "llama-phone", Only: []string{"src-b"}}
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "а дальше?", Pick: pick})
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	ps := payloads(t, b)
	last := ps[len(ps)-1]
	if len(pk.set) != 1 || pk.set[0] != "w-1=llama-phone" || last.Kind != mailrelay.KindAnswer || last.Pick == nil || last.Pick.Model != "llama-phone" {
		t.Fatalf("set %v, answer %+v", pk.set, last)
	}
	pk.refuse = errors.New("эта модель не разрешена телефону")
	asked := len(a.asked)
	b.phone("c-1", "A", 3, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "еще", Pick: &mailrelay.Pick{Model: "gpt"}})
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	ps = payloads(t, b)
	last = ps[len(ps)-1]
	if last.Kind != mailrelay.KindError || last.Text != "эта модель не разрешена телефону" || len(a.asked) != asked {
		t.Errorf("refused pick: %+v, turns %d → %d", last, asked, len(a.asked))
	}
	// Выбор в новом чате, которого окно еще не знает, — мимо: выбирать там не из чего.
	b.phone("c-new", "A", 1, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "новый", Pick: &mailrelay.Pick{Model: "llama-phone"}})
	pk.refuse = nil
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(pk.set) != 1 {
		t.Errorf("pick applied to a chat the window does not have: %v", pk.set)
	}
}
