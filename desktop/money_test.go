package main

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func gatewayPrices(t *testing.T) func(context.Context) (map[string]domain.ModelPrice, error) {
	t.Helper()
	p, err := domain.ParseModelPricePerMillion("2", "10")
	if err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (map[string]domain.ModelPrice, error) {
		return map[string]domain.ModelPrice{"anthropic/claude-sonnet-5.5": p}, nil
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// «Деньги» (#493): цена — своя правка, иначе шлюз; модель без цены считается
// отдельно, а не бесплатной; лимит месяца — в настройках окна.
func TestChat_Money(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	rec := &callsRec{}
	d.calls = rec
	d.prices = gatewayPrices(t)
	now := d.now()
	a, _ := domain.NewModelCall(now.Add(-time.Hour), domain.CallSearch, "anthropic/claude-sonnet-5.5", 1_000_000, 0, true, time.Second, time.Second, false)
	b, _ := domain.NewModelCall(now.Add(-time.Hour), domain.CallAnswer, "qwen3", 9000, 900, true, time.Second, time.Second, false)
	rec.calls = []domain.ModelCall{a, b}

	v, err := d.StatsMoney()
	if err != nil {
		t.Fatal(err)
	}
	if !near(v.Spent, 2) || v.Unpriced != 1 || v.Month != "2026-11" || v.DaysInMonth != 30 || v.Limit != "" {
		t.Fatalf("месяц = %+v", v)
	}
	if m := v.Models[0]; m.ID != "anthropic/claude-sonnet-5.5" || m.Source != "шлюз" || m.In != "2" || m.Out != "10" || !near(m.Cost, 2) {
		t.Fatalf("модель = %+v", m)
	}
	if p := v.Purposes[0]; p.ID != "search" || p.Title != "Поиск моделью" {
		t.Fatalf("назначение = %+v", p)
	}

	// Своя цена главнее шлюза; пустая — снова цена шлюза.
	if err := d.SetModelPrice("qwen3", "0", "0"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetModelPrice("anthropic/claude-sonnet-5.5", "1", "5"); err != nil {
		t.Fatal(err)
	}
	v, _ = d.StatsMoney()
	if !near(v.Spent, 1) || v.Unpriced != 0 {
		t.Fatalf("со своими ценами = %+v", v)
	}
	if err := d.SetModelPrice("anthropic/claude-sonnet-5.5", "", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.StatsMoney(); !near(v.Spent, 2) {
		t.Fatalf("после снятия своей цены = %v", v.Spent)
	}
	if err := d.SetModelPrice("qwen3", "дешево", "0"); !errors.Is(err, domain.ErrInvalidModelPrice) {
		t.Fatalf("испорченная цена: %v", err)
	}

	if err := d.SetSpendLimit("20"); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.StatsMoney(); v.Limit != "20" {
		t.Fatalf("лимит = %q", v.Limit)
	}
	for _, bad := range []string{"-1", "NaN", "0x10", "1e3", "Inf", "1.2.3"} {
		if err := d.SetSpendLimit(bad); err == nil {
			t.Fatalf("лимит %q принят", bad)
		}
	}
	if err := d.SetSpendLimit("12,5"); err != nil {
		t.Fatalf("12,5: %v", err)
	}
	if err := d.SetSpendLimit(""); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.StatsMoney(); v.Limit != "" {
		t.Fatalf("лимит не снят: %q", v.Limit)
	}
}

// Шлюз не ответил — «Деньги» открываются по своим ценам и говорят почему.
func TestChat_Money_gatewayDown(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	d.calls = &callsRec{}
	d.prices = func(context.Context) (map[string]domain.ModelPrice, error) {
		return map[string]domain.ModelPrice{}, errors.New("шлюз не ответил")
	}
	v, err := d.StatsMoney()
	if err != nil || v.PriceNote == "" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

// Модель, у которой за месяц только вызовы без счета (облачная речь), цену не
// получает: своя цена ничего бы не поменяла, поле ввода было бы обманом.
func TestChat_Money_uncountedOnlyModelIsNotPriceable(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	rec := &callsRec{}
	d.calls = rec
	d.prices = gatewayPrices(t)
	c, _ := domain.NewModelCall(d.now().Add(-time.Hour), domain.CallListen, "openai/gpt-4o-mini-transcribe", 0, 0, false, time.Second, time.Second, false)
	a, _ := domain.NewModelCall(d.now().Add(-time.Hour), domain.CallSearch, "anthropic/claude-sonnet-5.5", 1000, 0, true, time.Second, time.Second, false)
	rec.calls = []domain.ModelCall{c, a}
	v, err := d.StatsMoney()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range v.Models {
		if want := m.ID == "anthropic/claude-sonnet-5.5"; m.Priceable != want {
			t.Fatalf("%s: priceable=%v", m.ID, m.Priceable)
		}
	}
}
