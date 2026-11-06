package portrait_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
)

type memStore struct {
	p     domain.Portrait
	saves int
}

func (m *memStore) Load(context.Context) (domain.Portrait, error) { return m.p, nil }
func (m *memStore) Save(_ context.Context, p domain.Portrait) error {
	m.p = p
	m.saves++
	return nil
}

type fakeModel struct {
	reply        string
	system, user string
}

func (f *fakeModel) Complete(_ context.Context, system, user string) (string, error) {
	f.system, f.user = system, user
	return f.reply, nil
}

var now = time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC)

func newSvc(t *testing.T, m portrait.Model) (*portrait.Service, *memStore) {
	t.Helper()
	st := &memStore{}
	n := 0
	ids := func() string { n++; return "f" + string(rune('0'+n)) }
	return portrait.NewService(st, m, func() time.Time { return now }, ids), st
}

func sect(t *testing.T, raw string) domain.FactSection {
	t.Helper()
	s, err := domain.NewFactSection(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Вручную: добавленное сразу одобрено, правка и одобрение с правкой
// помечают источник, забытое исчезает; каждое изменение сохранено.
func TestService_Manual(t *testing.T) {
	ctx := t.Context()
	s, st := newSvc(t, &fakeModel{})
	f, err := s.Add(ctx, sect(t, "work"), "Пишет на Go")
	if err != nil || !f.Approved() || f.Source() != "добавлено вручную" {
		t.Fatalf("add: %+v %v", f, err)
	}
	if err := s.Edit(ctx, f.ID(), "Пишет на Go и TS"); err != nil {
		t.Fatal(err)
	}
	if got := st.p.Approved()[0]; got.Text() != "Пишет на Go и TS" || got.Source() != "исправлено вручную" {
		t.Fatalf("edit: %+v", got)
	}
	pend, _ := domain.NewFact("p1", sect(t, "prefs"), "Отвечать коротко", "из чата 06.11", domain.FactPending, now)
	st.p, _ = st.p.Add(pend)
	if err := s.Approve(ctx, "p1", "Отвечать коротко, без вступлений"); err != nil {
		t.Fatal(err)
	}
	if a := st.p.Approved(); len(a) != 2 || a[1].Text() != "Отвечать коротко, без вступлений" || a[1].Source() != "из чата 06.11, исправлено" {
		t.Fatalf("approve: %+v", a)
	}
	if err := s.Remove(ctx, f.ID()); err != nil || len(st.p.Facts()) != 1 {
		t.Fatalf("remove: %v", err)
	}
	if st.saves != 4 {
		t.Fatalf("сохранений %d", st.saves)
	}
}

// Разбор после ответа: модель отвечает JSON (бывает в ```), новое — в «ждут
// одобрения» с источником «из чата», чужой раздел и уже известное — мимо;
// модель видит обмен и то, что уже известно.
func TestService_Extract(t *testing.T) {
	ctx := t.Context()
	m := &fakeModel{reply: "```json\n[{\"section\":\"prefs\",\"text\":\"Отвечать коротко\"},{\"section\":\"secrets\",\"text\":\"x\"},{\"section\":\"work\",\"text\":\"пишет на go\"}]\n```"}
	s, st := newSvc(t, m)
	if _, err := s.Add(ctx, sect(t, "work"), "Пишет на Go"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Extract(ctx, "Сделай коротко, как обычно", "Готово: три шага.")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text() != "Отвечать коротко" || got[0].Approved() || got[0].Source() != "из чата 06.11" {
		t.Fatalf("новое = %+v", got)
	}
	if len(st.p.Pending()) != 1 {
		t.Fatalf("ждут одобрения: %d", len(st.p.Pending()))
	}
	for _, want := range []string{"Сделай коротко, как обычно", "Готово: три шага.", "Пишет на Go"} {
		if !strings.Contains(m.user, want) {
			t.Errorf("модель не видела %q", want)
		}
	}
	m.reply = "[]"
	if got, err := s.Extract(ctx, "Сколько стоит?", "100"); err != nil || len(got) != 0 {
		t.Fatalf("пусто: %+v %v", got, err)
	}
	m.reply = "не JSON вовсе"
	if _, err := s.Extract(ctx, "q", "a"); !errors.Is(err, portrait.ErrModelReply) {
		t.Fatalf("мусор: %v", err)
	}
}

// Сжатие — предложение, которое человек принимает: одобренное заменяется
// сжатым, ждущее одобрения остается.
func TestService_Compress(t *testing.T) {
	ctx := t.Context()
	m := &fakeModel{reply: `[{"section":"work","text":"Go, TypeScript; база знаний kb-engine"}]`}
	s, st := newSvc(t, m)
	_, _ = s.Add(ctx, sect(t, "work"), "Пишет на Go")
	_, _ = s.Add(ctx, sect(t, "work"), "Пишет на TypeScript")
	pend, _ := domain.NewFact("p1", sect(t, "plans"), "Выпуск до НГ", "из чата", domain.FactPending, now)
	st.p, _ = st.p.Add(pend)
	prop, err := s.Compress(ctx)
	if err != nil || len(prop) != 1 || prop[0].Text != "Go, TypeScript; база знаний kb-engine" {
		t.Fatalf("предложение: %+v %v", prop, err)
	}
	if len(st.p.Approved()) != 2 {
		t.Fatal("сжатие применилось без согласия")
	}
	if err := s.ApplyCompressed(ctx, prop); err != nil {
		t.Fatal(err)
	}
	if a, p := st.p.Approved(), st.p.Pending(); len(a) != 1 || a[0].Source() != "сжато" || len(p) != 1 {
		t.Fatalf("после: %+v / %+v", a, p)
	}
}

// В чат: помещается — целиком; нет — пусто и fits=false, пока человек не
// выберет «целиком» или «только нужное» (кто я и предпочтения всегда, остальное — по словам вопроса).
func TestContext(t *testing.T) {
	mk := func(id, s, text string) domain.Fact {
		sec, _ := domain.NewFactSection(s)
		f, _ := domain.NewFact(id, sec, text, "src", domain.FactApproved, now)
		return f
	}
	p, _ := domain.NewPortrait([]domain.Fact{
		mk("a", "about", "Разработчик"), mk("b", "prefs", "Отвечать коротко"),
		mk("c", "work", "Настраивает голос окна DVK"), mk("d", "plans", "Переезд весной"),
	})
	full := domain.PortraitText(p.Approved())
	if c := portrait.Context(p, "вопрос", 1000, portrait.ContextAuto); !c.Fits || c.Text != full || c.Size != len([]rune(full)) {
		t.Fatalf("помещается: %+v", c)
	}
	if c := portrait.Context(p, "вопрос", 20, portrait.ContextAuto); c.Fits || c.Text != "" {
		t.Fatalf("не помещается: %+v", c)
	}
	if c := portrait.Context(p, "вопрос", 20, portrait.ContextFull); c.Text != full {
		t.Fatalf("целиком: %+v", c)
	}
	c := portrait.Context(p, "Как настроить голос?", 20, portrait.ContextRelevant)
	if !strings.Contains(c.Text, "Разработчик") || !strings.Contains(c.Text, "Отвечать коротко") || !strings.Contains(c.Text, "голос окна") || strings.Contains(c.Text, "Переезд") {
		t.Fatalf("только нужное: %q", c.Text)
	}
}
