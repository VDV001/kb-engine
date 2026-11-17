package main

import (
	"context"
	"testing"
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
	w := windowTurn(got[len(got)-1])
	if !w.New || w.Chat != rec.ID || w.Title != rec.Title || len(w.History) != 3 || w.History[0].Question != "сроки поставки?" || w.History[2].Question != "а дальше?" || w.History[2].Answer == "" {
		t.Fatalf("turn %+v", w)
	}
	// Чат, рожденный на телефоне, Phone не получает: он и так связан с ящиком.
	ph, err := cd.ask(context.Background(), askReq{Question: "с телефона новый"}, askOut{worker: true})
	if err != nil || ph.Phone {
		t.Errorf("phone-born chat marked: %v %v", ph.Phone, err)
	}
}
