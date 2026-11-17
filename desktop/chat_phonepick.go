package main

import (
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
)

// SetPhonePick keeps the phone's choice for its turns in chat id (#657,
// решения владельца 17.11): модель — только из «Телефону: разрешить» ("" —
// модель окна); источники — только сужение выбора окна (all — как в окне).
func (d *chatDesk) SetPhonePick(id, model string, only []string, all bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, err := d.load(id)
	if err != nil {
		return err
	}
	if model != "" && !slices.Contains(d.config().PhoneModels, model) {
		return domain.ErrModelNotForPhone
	}
	rec.PhonePick, rec.PhoneOnly = model, nil
	if !all {
		keep := narrowed(only, d.phoneAllowed(rec))
		if len(keep) == 0 {
			return domain.ErrNoChatSources
		}
		rec.PhoneOnly = keep
	}
	return d.saveLive(rec, true)
}

// turnConfig is the chat settings and sources of a turn: ход окна — по выбору
// окна, ход телефона — по своему выбору, проверенному здесь же (phoneTurn).
func (d *chatDesk) turnConfig(out askOut, rec chatRecord, sources []string) (desktopconfig.Chat, []string, error) {
	c := d.config()
	if !out.worker {
		return c, sources, nil
	}
	return d.phoneTurn(c, rec)
}

// phoneTurn applies the phone's choice where the model and the sources are
// used: отозванная модель и пустое сужение — отказ до вызова модели, а не
// молча модель окна или «все» источники.
func (d *chatDesk) phoneTurn(c desktopconfig.Chat, rec chatRecord) (desktopconfig.Chat, []string, error) {
	sources := rec.Sources
	if rec.PhonePick != "" {
		if !slices.Contains(c.PhoneModels, rec.PhonePick) {
			return c, nil, domain.ErrModelNotForPhone
		}
		c.Model = rec.PhonePick
	}
	if rec.PhoneOnly != nil {
		if sources = narrowed(rec.PhoneOnly, d.phoneAllowed(rec)); len(sources) == 0 {
			return c, nil, domain.ErrNoChatSources
		}
	}
	return c, sources, nil
}

// phoneAllowed is what the phone may pick for chat rec: то, что окно ищет и
// дает чату (как показывает телефону phoneDesk), в пределах выбора окна.
func (d *chatDesk) phoneAllowed(rec chatRecord) []string {
	d.settings.mu.Lock()
	cfg := d.settings.cfg
	d.settings.mu.Unlock()
	ids := phoneSourceIDs(cfg)
	if len(rec.Sources) > 0 {
		ids = narrowed(rec.Sources, ids)
	}
	return ids
}

// narrowed keeps the ids of want that are in allowed, в порядке want.
func narrowed(want, allowed []string) []string {
	out := []string{}
	for _, id := range want {
		if slices.Contains(allowed, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
