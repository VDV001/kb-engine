package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Портрет человека (#479, решение владельца 06.11): что DVK помнит о нем.
// Человек видит каждый факт и откуда он, правит и забывает; новое из чатов
// сначала ждет его одобрения и до того в чат не идет.

// ErrInvalidFact — факт нарушает инвариант (пустой текст, чужой раздел…).
var ErrInvalidFact = errors.New("invalid fact")

// ErrFactExists — такой факт уже есть: тот же id или тот же текст в разделе.
var ErrFactExists = errors.New("fact already exists")

// ErrFactNotFound — факта с таким id в портрете нет.
var ErrFactNotFound = errors.New("fact not found")

// MaxFactRunes — потолок текста факта: факт — одна мысль, а не заметка.
const MaxFactRunes = 300

// FactSection — раздел портрета.
type FactSection string

// Разделы — как в english-hub, в порядке показа.
const (
	SectionAbout FactSection = "about"
	SectionWork  FactSection = "work"
	SectionPlans FactSection = "plans"
	SectionPrefs FactSection = "prefs"
	SectionMisc  FactSection = "misc"
)

var sectionTitles = map[FactSection]string{
	SectionAbout: "Обо мне", SectionWork: "Работа", SectionPlans: "Планы",
	SectionPrefs: "Предпочтения", SectionMisc: "Разное",
}

// FactSections lists the sections in display order.
func FactSections() []FactSection {
	return []FactSection{SectionAbout, SectionWork, SectionPlans, SectionPrefs, SectionMisc}
}

// NewFactSection validates a section name.
func NewFactSection(raw string) (FactSection, error) {
	s := FactSection(strings.TrimSpace(raw))
	if _, ok := sectionTitles[s]; !ok {
		return "", fmt.Errorf("%w: section %q", ErrInvalidFact, raw)
	}
	return s, nil
}

// Title is the section name a person reads.
func (s FactSection) Title() string { return sectionTitles[s] }

// FactStatus — одобрен ли факт человеком.
type FactStatus string

// Статусы факта: новое из чатов ждет одобрения, в чат идет только одобренное.
const (
	FactPending  FactStatus = "pending"
	FactApproved FactStatus = "approved"
)

// Fact is one thing DVK remembers about the person.
type Fact struct {
	id      string
	section FactSection
	text    string
	source  string
	status  FactStatus
	at      time.Time
}

// NewFact validates a fact; source — откуда он («из чата 06.11», «добавлено вручную»).
func NewFact(id string, section FactSection, text, source string, status FactStatus, at time.Time) (Fact, error) {
	if strings.TrimSpace(id) == "" {
		return Fact{}, fmt.Errorf("%w: id is required", ErrInvalidFact)
	}
	if _, ok := sectionTitles[section]; !ok {
		return Fact{}, fmt.Errorf("%w: section %q", ErrInvalidFact, section)
	}
	if status != FactPending && status != FactApproved {
		return Fact{}, fmt.Errorf("%w: status %q", ErrInvalidFact, status)
	}
	t, err := factText(text)
	if err != nil {
		return Fact{}, err
	}
	return Fact{id: id, section: section, text: t, source: strings.TrimSpace(source), status: status, at: at}, nil
}

func factText(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	switch {
	case t == "":
		return "", fmt.Errorf("%w: text is required", ErrInvalidFact)
	case utf8.RuneCountInString(t) > MaxFactRunes:
		return "", fmt.Errorf("%w: text longer than %d", ErrInvalidFact, MaxFactRunes)
	case strings.IndexFunc(t, unicode.IsControl) >= 0:
		return "", fmt.Errorf("%w: text has control characters", ErrInvalidFact)
	}
	return t, nil
}

// ID returns the fact id.
func (f Fact) ID() string { return f.id }

// Section returns the fact section.
func (f Fact) Section() FactSection { return f.section }

// Text returns the fact text.
func (f Fact) Text() string { return f.text }

// Source returns where the fact came from.
func (f Fact) Source() string { return f.source }

// Status returns whether the person approved the fact.
func (f Fact) Status() FactStatus { return f.status }

// Approved reports whether the fact goes to the chat.
func (f Fact) Approved() bool { return f.status == FactApproved }

// At returns when the fact appeared or changed.
func (f Fact) At() time.Time { return f.at }

// Portrait is everything DVK remembers about the person.
type Portrait struct {
	facts []Fact
}

// NewPortrait builds a portrait; повтор id или текста в разделе — отказ.
func NewPortrait(facts []Fact) (Portrait, error) {
	p := Portrait{}
	for _, f := range facts {
		var err error
		if p, err = p.Add(f); err != nil {
			return Portrait{}, err
		}
	}
	return p, nil
}

func sameText(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// Add returns the portrait with f; тот же id или тот же текст в разделе — ErrFactExists.
func (p Portrait) Add(f Fact) (Portrait, error) {
	for _, g := range p.facts {
		if g.id == f.id || (g.section == f.section && sameText(g.text, f.text)) {
			return p, fmt.Errorf("%w: %q", ErrFactExists, f.text)
		}
	}
	return Portrait{facts: append(slices.Clone(p.facts), f)}, nil
}

func (p Portrait) index(id string) (int, error) {
	i := slices.IndexFunc(p.facts, func(f Fact) bool { return f.id == id })
	if i < 0 {
		return -1, fmt.Errorf("%w: %q", ErrFactNotFound, id)
	}
	return i, nil
}

// Approve returns the portrait with the fact approved by the person.
func (p Portrait) Approve(id string) (Portrait, error) {
	i, err := p.index(id)
	if err != nil {
		return p, err
	}
	facts := slices.Clone(p.facts)
	facts[i].status = FactApproved
	return Portrait{facts: facts}, nil
}

// Edit returns the portrait with the fact text changed; source — новая пометка
// («исправлено вручную»).
func (p Portrait) Edit(id, text, source string) (Portrait, error) {
	i, err := p.index(id)
	if err != nil {
		return p, err
	}
	t, err := factText(text)
	if err != nil {
		return p, err
	}
	for j, g := range p.facts {
		if j != i && g.section == p.facts[i].section && sameText(g.text, t) {
			return p, fmt.Errorf("%w: %q", ErrFactExists, t)
		}
	}
	facts := slices.Clone(p.facts)
	facts[i].text, facts[i].source = t, strings.TrimSpace(source)
	return Portrait{facts: facts}, nil
}

// Remove returns the portrait without the fact: человек его забыл.
func (p Portrait) Remove(id string) (Portrait, error) {
	i, err := p.index(id)
	if err != nil {
		return p, err
	}
	return Portrait{facts: slices.Delete(slices.Clone(p.facts), i, i+1)}, nil
}

// Facts returns all facts in the order they appeared.
func (p Portrait) Facts() []Fact { return slices.Clone(p.facts) }

// Approved returns the facts that go to the chat.
func (p Portrait) Approved() []Fact {
	return slices.DeleteFunc(slices.Clone(p.facts), func(f Fact) bool { return !f.Approved() })
}

// Pending returns the facts waiting for the person's approval.
func (p Portrait) Pending() []Fact {
	return slices.DeleteFunc(slices.Clone(p.facts), func(f Fact) bool { return f.Approved() })
}

// PortraitText renders facts for the model: по разделу в строку, в порядке
// разделов, — «Обо мне: Разработчик; Готовится к роли тимлида.».
func PortraitText(facts []Fact) string {
	var lines []string
	for _, s := range FactSections() {
		var parts []string
		for _, f := range facts {
			if f.section == s {
				parts = append(parts, strings.TrimRight(f.text, "."))
			}
		}
		if len(parts) > 0 {
			lines = append(lines, s.Title()+": "+strings.Join(parts, "; ")+".")
		}
	}
	return strings.Join(lines, "\n")
}
