package main

import (
	"context"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/imagegen"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// imageDrawer draws a picture; imagegen.Client — настоящий, в тестах подставной.
type imageDrawer interface {
	Generate(ctx context.Context, prompt string, refs [][]byte) (imagegen.Image, error)
}

// imageDesk is the person's image model (#678): рисует и записывает каждый
// вызов в журнал расходов, чтобы статистика видела и изображения.
type imageDesk struct {
	draw  imageDrawer
	model string
	calls usage.Recorder // nil — не считать
	now   func() time.Time
}

// Generate draws a picture and journals the call: цена шлюза — оплаченным
// вызовом, нет цены — «без счета», сбой — с пометкой.
func (d imageDesk) Generate(ctx context.Context, prompt string, refs [][]byte) (imagegen.Image, error) {
	now := d.now
	if now == nil {
		now = time.Now
	}
	start := now()
	img, err := d.draw.Generate(ctx, prompt, refs)
	d.record(start, max(now().Sub(start), 0), err != nil, img.Cost)
	return img, err
}

func (d imageDesk) record(start time.Time, took time.Duration, failed bool, cost string) {
	if d.calls == nil {
		return
	}
	var call domain.ModelCall
	var err error
	if pico, perr := domain.ParseDollars(cost); perr == nil {
		call, err = domain.NewBilledModelCall(start, domain.CallImage, d.model, pico, took, failed)
	} else {
		call, err = domain.NewModelCall(start, domain.CallImage, d.model, 0, 0, false, took, took, failed)
	}
	if err == nil {
		_ = d.calls.Record(call)
	}
}
