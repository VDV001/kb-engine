package main

import (
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func call(t *testing.T, at time.Time, model string) domain.ModelCall {
	t.Helper()
	c, err := domain.NewModelCall(at, domain.CallAnswer, model, 10, 5, true, time.Second, 2*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func price(t *testing.T, in, out string) domain.ModelPrice {
	t.Helper()
	p, err := domain.ParseModelPricePerMillion(in, out)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestModelChoices(t *testing.T) {
	day := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	calls := []domain.ModelCall{
		call(t, day, "anthropic/claude-sonnet-5.5"),
		call(t, day.Add(time.Hour), "anthropic/claude-haiku-5.5"),
		call(t, day.Add(2*time.Hour), "openai/gpt-5-mini"),
		call(t, day.Add(3*time.Hour), "anthropic/claude-haiku-5.5"),
		call(t, day.Add(4*time.Hour), "google/gemini-3-flash"),
	}
	gw := map[string]domain.ModelPrice{
		"anthropic/claude-haiku-5.5":  price(t, "1", "5"),
		"anthropic/claude-sonnet-5.5": price(t, "3", "15"),
		"openai/gpt-5-mini":           price(t, "0.25", "2"),
	}
	own := map[string]domain.ModelPrice{"anthropic/claude-haiku-5.5": price(t, "0.8", "4")}

	v := modelChoices("anthropic/claude-haiku-5.5", pricesURL, calls, gw, own)
	if v.Current != "anthropic/claude-haiku-5.5" || !v.Gateway {
		t.Errorf("current = %q, gateway = %v", v.Current, v.Gateway)
	}
	recent := []string{}
	for _, m := range v.Recent {
		recent = append(recent, m.ID)
	}
	// Последние по времени вызова, без повторов, не больше трех.
	if want := []string{"google/gemini-3-flash", "anthropic/claude-haiku-5.5", "openai/gpt-5-mini"}; !slices.Equal(recent, want) {
		t.Errorf("recent = %v, want %v", recent, want)
	}
	all := []string{}
	for _, m := range v.All {
		all = append(all, m.ID)
	}
	if !slices.IsSorted(all) || !slices.Contains(all, "anthropic/claude-sonnet-5.5") || !slices.Contains(all, "google/gemini-3-flash") {
		t.Errorf("all = %v, want sorted, gateway and used models", all)
	}
	for _, m := range v.All {
		switch m.ID {
		case "anthropic/claude-haiku-5.5":
			if m.In != "0.8" || m.Out != "4" || !m.Own {
				t.Errorf("own price first: %+v", m)
			}
		case "openai/gpt-5-mini":
			if m.In != "0.25" || m.Out != "2" {
				t.Errorf("gateway price: %+v", m)
			}
		case "google/gemini-3-flash":
			if m.In != "" || m.Out != "" {
				t.Errorf("unknown price must stay empty: %+v", m)
			}
		}
	}

	// Не шлюз Vercel: его список моделей к этому адресу не относится.
	local := modelChoices("llama3.2", "http://127.0.0.1:11434/v1", calls, gw, nil)
	if local.Gateway {
		t.Error("local address treated as the gateway")
	}
	for _, m := range local.All {
		if m.ID == "anthropic/claude-sonnet-5.5" && m.In == "" {
			continue
		}
		if _, used := gw[m.ID]; used && !slices.ContainsFunc(calls, func(c domain.ModelCall) bool { return c.Model() == m.ID }) {
			t.Errorf("gateway-only model %q offered for a local address", m.ID)
		}
	}
	if !slices.ContainsFunc(local.All, func(m modelChoice) bool { return m.ID == "llama3.2" }) {
		t.Error("current model missing from the list")
	}
}

func TestSetChatModel(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	before := d.Settings()
	if err := d.SetModel("  openai/gpt-5-mini "); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	after := d.Settings()
	if after.Model != "openai/gpt-5-mini" {
		t.Errorf("model = %q", after.Model)
	}
	if after.URL != before.URL || after.Chat.Mode != before.Chat.Mode || after.Voice != before.Voice {
		t.Errorf("other settings changed: before %+v, after %+v", before, after)
	}
	if err := d.SetModel("   "); err == nil {
		t.Error("empty model saved")
	}
}
