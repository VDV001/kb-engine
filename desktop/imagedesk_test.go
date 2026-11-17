package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/imagegen"
	"github.com/daniil/kb-engine/internal/domain"
)

type memCalls struct{ calls []domain.ModelCall }

func (m *memCalls) Record(c domain.ModelCall) error { m.calls = append(m.calls, c); return nil }

type fakeDraw struct {
	img imagegen.Image
	err error
}

func (f fakeDraw) Generate(context.Context, string, [][]byte) (imagegen.Image, error) {
	return f.img, f.err
}

// Каждый вызов модели изображений попадает в журнал назначением «image»: с
// ценой шлюза — оплаченным, без цены — «без счета», сбой — с пометкой (#678).
func TestImageDeskRecordsEveryCall(t *testing.T) {
	calls := &memCalls{}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	d := imageDesk{draw: fakeDraw{img: imagegen.Image{Data: []byte("p"), MIME: "image/png", Cost: "0.068"}}, model: "google/gemini-3.1-flash-image", calls: calls, now: func() time.Time { return now }}
	if _, err := d.Generate(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
	d.draw = fakeDraw{img: imagegen.Image{Data: []byte("p"), MIME: "image/png"}}
	if _, err := d.Generate(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
	d.draw = fakeDraw{err: errors.New("отказ")}
	if _, err := d.Generate(context.Background(), "x", nil); err == nil {
		t.Fatal("a failure swallowed")
	}
	if len(calls.calls) != 3 {
		t.Fatalf("calls %d", len(calls.calls))
	}
	if c := calls.calls[0]; c.Purpose() != domain.CallImage || !c.Billed() || c.Cost() != 68_000_000_000 || c.Model() != "google/gemini-3.1-flash-image" {
		t.Errorf("billed call %+v", c)
	}
	if c := calls.calls[1]; c.Billed() || c.Failed() {
		t.Errorf("no price: %+v", c)
	}
	if !calls.calls[2].Failed() {
		t.Error("the failure is not marked")
	}
	if purposeTitles[domain.CallImage] != "Изображения" {
		t.Errorf("title %q", purposeTitles[domain.CallImage])
	}
}
