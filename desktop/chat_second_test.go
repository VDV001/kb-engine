package main

import (
	"context"
	"sync"
	"testing"
)

// Вторая модель чата (#678): человек выбирает главную и второстепенную; ход по
// роли «second» идет на второй, без нее или без второй — на главной.
func TestAskRoutesToTheSecondModel(t *testing.T) {
	d, _, used, id := pickDesk(t)
	if err := d.SetSecondModel("llama-phone"); err != nil {
		t.Fatal(err)
	}
	if got := d.config().SecondModel; got != "llama-phone" {
		t.Fatalf("second model %q", got)
	}
	rec, err := d.ask(context.Background(), askReq{ID: id, Question: "привет", Role: "second"}, askOut{})
	if err != nil || used.Model != "llama-phone" {
		t.Fatalf("second turn on %q, %v", used.Model, err)
	}
	if last := rec.Messages[len(rec.Messages)-1]; last.Model != "llama-phone" {
		t.Errorf("the answer is signed by %q", last.Model)
	}
	rec, err = d.ask(context.Background(), askReq{ID: id, Question: "дальше"}, askOut{})
	if err != nil || used.Model != "qwen3" || rec.Messages[len(rec.Messages)-1].Model != "qwen3" {
		t.Errorf("main turn on %q, %v", used.Model, err)
	}
	if err := d.SetSecondModel(""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ask(context.Background(), askReq{ID: id, Question: "снова", Role: "second"}, askOut{}); err != nil || used.Model != "qwen3" {
		t.Errorf("no second model set: turn on %q, %v", used.Model, err)
	}
}

// Подсказка: какой моделью решить вопрос — правилами, с названием модели и
// причиной словами; второй модели нет — подсказки нет.
func TestChatHint(t *testing.T) {
	d, _, _, _ := pickDesk(t)
	if h := d.Hint("привет", 0); h.Role != "" {
		t.Errorf("a hint without a second model: %+v", h)
	}
	if err := d.SetSecondModel("llama-phone"); err != nil {
		t.Fatal(err)
	}
	if h := d.Hint("привет", 0); h.Role != "second" || h.Model != "llama-phone" || h.Reason == "" {
		t.Errorf("short: %+v", h)
	}
	if h := d.Hint("Сравни два договора", 0); h.Role != "main" || h.Model != "qwen3" || h.Reason == "" {
		t.Errorf("analysis: %+v", h)
	}
	if h := d.Hint("  ", 0); h.Role != "" {
		t.Errorf("empty question: %+v", h)
	}
}

// Вторая модель — не голое что угодно: без управляющих символов.
func TestSecondModelIsChecked(t *testing.T) {
	d, _, _, _ := pickDesk(t)
	if err := d.SetSecondModel("a\nb"); err == nil {
		t.Error("control characters accepted")
	}
}

// Смена главной и выбор второй не теряют друг друга (ревью #682, п.4).
func TestSetModelAndSecondModelDoNotLoseEachOther(t *testing.T) {
	d, _, _, _ := pickDesk(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := d.SetModel("m-main"); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := d.SetSecondModel("m-second"); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if c := d.config(); c.Model != "m-main" || c.SecondModel != "m-second" {
		t.Errorf("model %q second %q", c.Model, c.SecondModel)
	}
}

// Вторая модель, ставшая главной, перестает быть второй: выбирать не из чего
// (ревью #682, п.7).
func TestSetModelClearsTheSameSecond(t *testing.T) {
	d, _, _, _ := pickDesk(t)
	if err := d.SetSecondModel("llama-phone"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetModel("llama-phone"); err != nil {
		t.Fatal(err)
	}
	if c := d.config(); c.Model != "llama-phone" || c.SecondModel != "" {
		t.Errorf("model %q second %q", c.Model, c.SecondModel)
	}
}
