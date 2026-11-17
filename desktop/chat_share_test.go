package main

import (
	"context"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// Чаты окна на телефоне (#669): новый чат окна помечен Phone и после каждого
// хода человека уходит работнику ящика со всей историей; ход с телефона
// обратно не отражается.
func TestWindowTurnsGoToThePhone(t *testing.T) {
	cd, _ := localChat(t)
	var got []chatRecord
	cd.turned = func(r chatRecord) { got = append(got, r) }
	rec, err := cd.Ask(askReq{Question: "сроки поставки?"})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Phone || len(got) != 1 {
		t.Fatalf("phone %v, turns %d", rec.Phone, len(got))
	}
	if _, err := cd.ask(context.Background(), askReq{ID: rec.ID, Question: "с телефона"}, askOut{worker: true}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("a phone turn echoed back to the phone: %d turns", len(got))
	}
	if _, err := cd.Ask(askReq{ID: rec.ID, Question: "а дальше?"}); err != nil {
		t.Fatal(err)
	}
	// В очереди — последний обмен; историю ящик читает из файла чата сам (ревью #675).
	w := windowTurn(got[len(got)-1])
	if !w.New || w.Chat != rec.ID || w.Title != rec.Title || w.Last.Question != "а дальше?" || w.Last.Answer == "" {
		t.Fatalf("turn %+v", w)
	}
	h, err := chatHistory(cd)(rec.ID)
	if err != nil || len(h) != 3 || h[0].Question != "сроки поставки?" || h[2].Question != "а дальше?" {
		t.Fatalf("history %+v %v", h, err)
	}
	// Чат, рожденный на телефоне, Phone не получает: он и так связан с ящиком.
	ph, err := cd.ask(context.Background(), askReq{Question: "с телефона новый"}, askOut{worker: true})
	if err != nil || ph.Phone {
		t.Errorf("phone-born chat marked: %v %v", ph.Phone, err)
	}
}

// Ходы сервера, чей круг сорвался до «легли в исходящие», ждут следующего
// круга этого сервера; другим серверам они не повторяются (ревью #675).
func TestWorkerKeepsTurnsOfAFailedRound(t *testing.T) {
	w := &mailWorker{}
	a := mailrelay.WindowTurn{Chat: "w-a"}
	b := mailrelay.WindowTurn{Chat: "w-b"}
	if q := w.turnsFor("s1", []mailrelay.WindowTurn{a}); len(q) != 1 {
		t.Fatalf("%v", q)
	}
	w.keepTurns("s1", []mailrelay.WindowTurn{a})
	q := w.turnsFor("s1", []mailrelay.WindowTurn{b})
	if len(q) != 2 || q[0].Chat != "w-a" || q[1].Chat != "w-b" {
		t.Fatalf("s1 after a failed round: %v", q)
	}
	if q := w.turnsFor("s2", []mailrelay.WindowTurn{b}); len(q) != 1 {
		t.Errorf("s2 got another server's kept turns: %v", q)
	}
	if q := w.turnsFor("s1", nil); len(q) != 0 {
		t.Errorf("kept turns given twice: %v", q)
	}
}
