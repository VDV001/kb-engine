package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

var factAt = time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC)

func mustFact(t *testing.T, id, section, text string, status domain.FactStatus) domain.Fact {
	t.Helper()
	s, err := domain.NewFactSection(section)
	if err != nil {
		t.Fatal(err)
	}
	f, err := domain.NewFact(id, s, text, domain.OriginChat, status, factAt)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Разделы портрета — пять известных, как в english-hub.
func TestNewFactSection(t *testing.T) {
	for _, raw := range []string{"about", "work", "plans", "prefs", "misc"} {
		if s, err := domain.NewFactSection(raw); err != nil || string(s) != raw {
			t.Errorf("%q: %q, %v", raw, s, err)
		}
	}
	if _, err := domain.NewFactSection("secrets"); !errors.Is(err, domain.ErrInvalidFact) {
		t.Fatalf("неизвестный раздел принят: %v", err)
	}
	if len(domain.FactSections()) != 5 {
		t.Fatalf("разделов %d", len(domain.FactSections()))
	}
}

// Происхождение факта — код, а не слова для человека: слова собирает окно
// (ревью #482/#483, UI-строки не в домене и не в файле портрета).
func TestNewFactOrigin(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want domain.FactOrigin
		ok   bool
	}{
		{"manual", domain.OriginManual, true},
		{"edited", domain.OriginEdited, true},
		{"chat", domain.OriginChat, true},
		{"chat-edited", domain.OriginChatEdited, true},
		{"compressed", domain.OriginCompressed, true},
		{" chat ", domain.OriginChat, true},
		{"", "", false},
		{"из чата 06.11", "", false},
		{"добавлено вручную", "", false},
	} {
		got, err := domain.NewFactOrigin(c.raw)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("%q: %q, %v", c.raw, got, err)
		}
		if !c.ok && !errors.Is(err, domain.ErrInvalidFact) {
			t.Errorf("%q принят: %q, %v", c.raw, got, err)
		}
	}
}

// Одобрение с правкой: факт из чата остается фактом из чата, только
// исправленным; остальное — исправлено вручную.
func TestFactOrigin_Corrected(t *testing.T) {
	for _, c := range []struct{ from, want domain.FactOrigin }{
		{domain.OriginChat, domain.OriginChatEdited},
		{domain.OriginChatEdited, domain.OriginChatEdited},
		{domain.OriginManual, domain.OriginEdited},
		{domain.OriginEdited, domain.OriginEdited},
		{domain.OriginCompressed, domain.OriginEdited},
	} {
		if got := c.from.Corrected(); got != c.want {
			t.Errorf("%q.Corrected() = %q, want %q", c.from, got, c.want)
		}
	}
}

// Факт — непустой текст без управляющих символов и не длиннее потолка, с id,
// известным происхождением и статусом; пробелы по краям срезаются.
func TestNewFact(t *testing.T) {
	about, _ := domain.NewFactSection("about")
	ok, err := domain.NewFact("f1", about, "  Пишет на Go  ", domain.OriginChat, domain.FactApproved, factAt)
	if err != nil || ok.Text() != "Пишет на Go" || ok.ID() != "f1" || ok.Section() != about || !ok.Approved() || ok.Origin() != domain.OriginChat {
		t.Fatalf("факт = %+v, %v", ok, err)
	}
	for _, c := range []struct {
		name, id, text string
		origin         domain.FactOrigin
		status         domain.FactStatus
	}{
		{"пустой текст", "f1", "   ", domain.OriginManual, domain.FactApproved},
		{"длинный", "f1", strings.Repeat("я", domain.MaxFactRunes+1), domain.OriginManual, domain.FactApproved},
		{"перевод строки", "f1", "раз\nдва", domain.OriginManual, domain.FactApproved},
		{"без id", "", "текст", domain.OriginManual, domain.FactApproved},
		{"чужой статус", "f1", "текст", domain.OriginManual, domain.FactStatus("maybe")},
		{"слова вместо кода", "f1", "текст", domain.FactOrigin("добавлено вручную"), domain.FactApproved},
		{"без происхождения", "f1", "текст", "", domain.FactApproved},
	} {
		if _, err := domain.NewFact(c.id, about, c.text, c.origin, c.status, factAt); !errors.Is(err, domain.ErrInvalidFact) {
			t.Errorf("%s: принят (%v)", c.name, err)
		}
	}
}

// Портрет: новое — в «ждут одобрения», одобренное — в портрете; правка,
// удаление, повтор того же текста в разделе — отказ.
func TestPortrait_Lifecycle(t *testing.T) {
	p, err := domain.NewPortrait(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.Add(mustFact(t, "f1", "work", "Пишет на Go", domain.FactApproved))
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.Add(mustFact(t, "f2", "prefs", "Отвечать коротко", domain.FactPending))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(mustFact(t, "f3", "work", "  пишет на go ", domain.FactPending)); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("повтор принят: %v", err)
	}
	if _, err := p.Add(mustFact(t, "f1", "misc", "другое", domain.FactPending)); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("повтор id принят: %v", err)
	}
	if len(p.Approved()) != 1 || len(p.Pending()) != 1 {
		t.Fatalf("одобрено %d, ждут %d", len(p.Approved()), len(p.Pending()))
	}
	if p, err = p.Approve("f2"); err != nil || len(p.Pending()) != 0 || len(p.Approved()) != 2 {
		t.Fatalf("одобрение: %v", err)
	}
	if p, err = p.Edit("f1", "Пишет на Go и TypeScript", domain.OriginEdited); err != nil || p.Approved()[0].Text() != "Пишет на Go и TypeScript" || p.Approved()[0].Origin() != domain.OriginEdited {
		t.Fatalf("правка: %+v %v", p.Approved(), err)
	}
	if _, err := p.Edit("f1", "  ", domain.OriginEdited); !errors.Is(err, domain.ErrInvalidFact) {
		t.Fatalf("пустая правка принята: %v", err)
	}
	if _, err := p.Edit("f1", "другой текст", "исправлено вручную"); !errors.Is(err, domain.ErrInvalidFact) {
		t.Fatalf("правка со словами вместо кода принята: %v", err)
	}
	if p, err = p.Remove("f1"); err != nil || len(p.Facts()) != 1 {
		t.Fatalf("удаление: %v", err)
	}
	for _, op := range []func() error{
		func() error { _, err := p.Remove("nope"); return err },
		func() error { _, err := p.Approve("nope"); return err },
		func() error { _, err := p.Edit("nope", "t", domain.OriginEdited); return err },
	} {
		if err := op(); !errors.Is(err, domain.ErrFactNotFound) {
			t.Errorf("нет такого факта: %v", err)
		}
	}
}

// Правка не может повторить другой факт раздела: такой портрет NewPortrait
// потом не прочтет, и весь портрет станет «испорченным» (ревью #482).
func TestPortrait_EditKeepsTextsUnique(t *testing.T) {
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	a, _ := domain.NewFact("a", domain.SectionWork, "Пишет на Go", domain.OriginManual, domain.FactApproved, at)
	b, _ := domain.NewFact("b", domain.SectionWork, "Пишет на Python", domain.OriginManual, domain.FactApproved, at)
	c, _ := domain.NewFact("c", domain.SectionPrefs, "Пишет на Python", domain.OriginManual, domain.FactApproved, at)
	p, err := domain.NewPortrait([]domain.Fact{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Edit("b", " пишет на go ", domain.OriginEdited); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("повтор в разделе = %v", err)
	}
	if _, err := p.Edit("a", "Пишет на Go", domain.OriginEdited); err != nil {
		t.Fatalf("тот же текст у самого факта: %v", err)
	}
	next, err := p.Edit("c", "Пишет на Go", domain.OriginEdited)
	if err != nil {
		t.Fatalf("тот же текст в другом разделе: %v", err)
	}
	if _, err := domain.NewPortrait(next.Facts()); err != nil {
		t.Fatalf("после правки портрет не читается: %v", err)
	}
}
