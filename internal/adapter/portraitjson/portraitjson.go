// Package portraitjson is the portrait (#479) on the wire and on disk: один
// формат у файла в папке человека, у объекта в бакете kb-сервера и у ответа
// сервера окну.
package portraitjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrCorrupt — данные не читаются как портрет: перезаписывать их пустым нельзя.
var ErrCorrupt = errors.New("portrait data is corrupt")

type fact struct {
	ID      string    `json:"id"`
	Section string    `json:"section"`
	Text    string    `json:"text"`
	Source  string    `json:"source"` // код происхождения (domain.FactOrigin)
	Status  string    `json:"status"`
	At      time.Time `json:"at"`
}

type doc struct {
	Facts []fact `json:"facts"`
}

// Encode writes the portrait as indented JSON.
func Encode(p domain.Portrait) ([]byte, error) {
	d := doc{Facts: []fact{}}
	for _, x := range p.Facts() {
		d.Facts = append(d.Facts, fact{x.ID(), string(x.Section()), x.Text(), string(x.Origin()), string(x.Status()), x.At()})
	}
	return json.MarshalIndent(d, "", "  ")
}

// Decode reads the portrait through the domain: каждый факт проверяется
// NewFact, портрет — NewPortrait.
func Decode(raw []byte) (domain.Portrait, error) {
	var d doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	facts := make([]domain.Fact, 0, len(d.Facts))
	for _, x := range d.Facts {
		f, err := domain.NewFact(x.ID, domain.FactSection(x.Section), x.Text, origin(x.Source), domain.FactStatus(x.Status), x.At)
		if err != nil {
			return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
		facts = append(facts, f)
	}
	p, err := domain.NewPortrait(facts)
	if err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return p, nil
}

// origin reads the origin code. Поле — только для подписи, поэтому чтение
// терпимое: подписи 0.36.0 переводятся в коды, хвост «, исправлено», который
// окно 0.36.0 дописывает к чужому коду, — в «исправлено», а незнакомое
// читается как ручной факт: иначе одно поле сделало бы испорченным весь портрет.
func origin(raw string) domain.FactOrigin {
	t := strings.TrimSpace(raw)
	base, edited := strings.CutSuffix(t, ", исправлено")
	var o domain.FactOrigin
	switch {
	case base == "добавлено вручную":
		o = domain.OriginManual
	case base == "исправлено вручную":
		o = domain.OriginEdited
	case base == "сжато":
		o = domain.OriginCompressed
	case strings.HasPrefix(base, "из чата"):
		o = domain.OriginChat
	default:
		var err error
		if o, err = domain.NewFactOrigin(base); err != nil {
			o = domain.OriginManual
		}
	}
	if edited {
		o = o.Corrected()
	}
	return o
}
