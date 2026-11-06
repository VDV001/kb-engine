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
	f, err := domain.NewFact(id, s, text, "из чата 06.11", status, factAt)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Разделы портрета — пять известных, как в english-hub; у каждого имя для человека.
func TestNewFactSection(t *testing.T) {
	for _, c := range []struct{ raw, title string }{
		{"about", "Обо мне"}, {"work", "Работа"}, {"plans", "Планы"}, {"prefs", "Предпочтения"}, {"misc", "Разное"},
	} {
		s, err := domain.NewFactSection(c.raw)
		if err != nil || s.Title() != c.title {
			t.Errorf("%q: %q, %v", c.raw, s.Title(), err)
		}
	}
	if _, err := domain.NewFactSection("secrets"); !errors.Is(err, domain.ErrInvalidFact) {
		t.Fatalf("неизвестный раздел принят: %v", err)
	}
	if len(domain.FactSections()) != 5 {
		t.Fatalf("разделов %d", len(domain.FactSections()))
	}
}

// Факт — непустой текст без управляющих символов и не длиннее потолка, с id
// и известным статусом; пробелы по краям срезаются.
func TestNewFact(t *testing.T) {
	about, _ := domain.NewFactSection("about")
	ok, err := domain.NewFact("f1", about, "  Пишет на Go  ", "из чата", domain.FactApproved, factAt)
	if err != nil || ok.Text() != "Пишет на Go" || ok.ID() != "f1" || ok.Section() != about || !ok.Approved() {
		t.Fatalf("факт = %+v, %v", ok, err)
	}
	for _, c := range []struct {
		name, id, text string
		status         domain.FactStatus
	}{
		{"пустой текст", "f1", "   ", domain.FactApproved},
		{"длинный", "f1", strings.Repeat("я", domain.MaxFactRunes+1), domain.FactApproved},
		{"перевод строки", "f1", "раз\nдва", domain.FactApproved},
		{"без id", "", "текст", domain.FactApproved},
		{"чужой статус", "f1", "текст", domain.FactStatus("maybe")},
	} {
		if _, err := domain.NewFact(c.id, about, c.text, "src", c.status, factAt); !errors.Is(err, domain.ErrInvalidFact) {
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
	if p, err = p.Edit("f1", "Пишет на Go и TypeScript", "исправлено вручную"); err != nil || p.Approved()[0].Text() != "Пишет на Go и TypeScript" || p.Approved()[0].Source() != "исправлено вручную" {
		t.Fatalf("правка: %+v %v", p.Approved(), err)
	}
	if _, err := p.Edit("f1", "  ", "x"); !errors.Is(err, domain.ErrInvalidFact) {
		t.Fatalf("пустая правка принята: %v", err)
	}
	if p, err = p.Remove("f1"); err != nil || len(p.Facts()) != 1 {
		t.Fatalf("удаление: %v", err)
	}
	for _, op := range []func() error{
		func() error { _, err := p.Remove("nope"); return err },
		func() error { _, err := p.Approve("nope"); return err },
		func() error { _, err := p.Edit("nope", "t", "s"); return err },
	} {
		if err := op(); !errors.Is(err, domain.ErrFactNotFound) {
			t.Errorf("нет такого факта: %v", err)
		}
	}
}

// Текст портрета для модели — только одобренное, по разделам в их порядке.
func TestPortrait_Text(t *testing.T) {
	p, _ := domain.NewPortrait([]domain.Fact{
		mustFact(t, "a", "prefs", "Отвечать коротко", domain.FactApproved),
		mustFact(t, "b", "about", "Разработчик", domain.FactApproved),
		mustFact(t, "c", "about", "Готовится к роли тимлида", domain.FactApproved),
		mustFact(t, "d", "work", "Тайное, ждет одобрения", domain.FactPending),
	})
	want := "Обо мне: Разработчик; Готовится к роли тимлида.\nПредпочтения: Отвечать коротко."
	if got := domain.PortraitText(p.Approved()); got != want {
		t.Fatalf("текст:\n%s\nwant:\n%s", got, want)
	}
	if domain.PortraitText(nil) != "" {
		t.Fatal("пустой портрет не пуст")
	}
}

// Правка не может повторить другой факт раздела: такой портрет NewPortrait
// потом не прочтет, и весь портрет станет «испорченным» (ревью #482).
func TestPortrait_EditKeepsTextsUnique(t *testing.T) {
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	a, _ := domain.NewFact("a", domain.SectionWork, "Пишет на Go", "", domain.FactApproved, at)
	b, _ := domain.NewFact("b", domain.SectionWork, "Пишет на Python", "", domain.FactApproved, at)
	c, _ := domain.NewFact("c", domain.SectionPrefs, "Пишет на Python", "", domain.FactApproved, at)
	p, err := domain.NewPortrait([]domain.Fact{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Edit("b", " пишет на go ", ""); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("повтор в разделе = %v", err)
	}
	if _, err := p.Edit("a", "Пишет на Go", "исправлено"); err != nil {
		t.Fatalf("тот же текст у самого факта: %v", err)
	}
	next, err := p.Edit("c", "Пишет на Go", "")
	if err != nil {
		t.Fatalf("тот же текст в другом разделе: %v", err)
	}
	if _, err := domain.NewPortrait(next.Facts()); err != nil {
		t.Fatalf("после правки портрет не читается: %v", err)
	}
}
