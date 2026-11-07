package portrait_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	if err != nil || !f.Approved() || f.Origin() != domain.OriginManual {
		t.Fatalf("add: %+v %v", f, err)
	}
	if err := s.Edit(ctx, f.ID(), "Пишет на Go и TS"); err != nil {
		t.Fatal(err)
	}
	if got := st.p.Approved()[0]; got.Text() != "Пишет на Go и TS" || got.Origin() != domain.OriginEdited {
		t.Fatalf("edit: %+v", got)
	}
	pend, _ := domain.NewFact("p1", sect(t, "prefs"), "Отвечать коротко", domain.OriginChat, domain.FactPending, now)
	st.p, _ = st.p.Add(pend)
	if err := s.Approve(ctx, "p1", "Отвечать коротко, без вступлений"); err != nil {
		t.Fatal(err)
	}
	if a := st.p.Approved(); len(a) != 2 || a[1].Text() != "Отвечать коротко, без вступлений" || a[1].Origin() != domain.OriginChatEdited {
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
	if len(got) != 1 || got[0].Text() != "Отвечать коротко" || got[0].Approved() || got[0].Origin() != domain.OriginChat || !got[0].At().Equal(now) {
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
	pend, _ := domain.NewFact("p1", sect(t, "plans"), "Выпуск до НГ", domain.OriginChat, domain.FactPending, now)
	st.p, _ = st.p.Add(pend)
	cmp, err := s.Compress(ctx)
	prop := cmp.Proposals
	if err != nil || len(prop) != 1 || prop[0].Text != "Go, TypeScript; база знаний kb-engine" || len(cmp.Basis) != 2 {
		t.Fatalf("предложение: %+v %v", cmp, err)
	}
	if cmp.Size != len([]rune("Работа: Go, TypeScript; база знаний kb-engine.")) {
		t.Fatalf("размер сжатого = %d — считать надо так, как текст уйдет в чат", cmp.Size)
	}
	if len(st.p.Approved()) != 2 {
		t.Fatal("сжатие применилось без согласия")
	}
	// Пока человек читал предложение, он одобрил еще факт: сжатие его не стирает (ревью #482).
	if _, err := s.Add(ctx, sect(t, "prefs"), "Отвечать коротко"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompressed(ctx, cmp); err != nil {
		t.Fatal(err)
	}
	a, p := st.p.Approved(), st.p.Pending()
	if len(a) != 2 || len(p) != 1 || !strings.Contains(portrait.Text(a), "Отвечать коротко") || !strings.Contains(portrait.Text(a), "база знаний") {
		t.Fatalf("после: %+v / %+v", a, p)
	}
	if !slices.ContainsFunc(a, func(f domain.Fact) bool { return f.Origin() == domain.OriginCompressed }) {
		t.Fatalf("сжатое не помечено происхождением «сжато»: %+v", a)
	}
}

// В чат: помещается — целиком; нет — пусто и fits=false, пока человек не
// выберет «целиком» или «только нужное» (кто я и предпочтения всегда, остальное — по словам вопроса).
func TestContext(t *testing.T) {
	mk := func(id, s, text string) domain.Fact {
		sec, _ := domain.NewFactSection(s)
		f, _ := domain.NewFact(id, sec, text, domain.OriginManual, domain.FactApproved, now)
		return f
	}
	p, _ := domain.NewPortrait([]domain.Fact{
		mk("a", "about", "Разработчик"), mk("b", "prefs", "Отвечать коротко"),
		mk("c", "work", "Настраивает голос окна DVK"), mk("d", "plans", "Переезд весной"),
	})
	full := portrait.Text(p.Approved())
	if c := portrait.Context(p, "вопрос", 1000, portrait.ContextAuto); !c.Fits || c.Text != full || c.Size != len([]rune(full)) {
		t.Fatalf("помещается: %+v", c)
	}
	if c := portrait.Context(p, "вопрос", 20, portrait.ContextAuto); c.Fits || c.Text != "" {
		t.Fatalf("не помещается: %+v", c)
	}
	if c := portrait.Context(p, "вопрос", 20, portrait.ContextFull); c.Text != full {
		t.Fatalf("целиком: %+v", c)
	}
	over := len([]rune(full)) - 1 // целиком не влезает
	c := portrait.Context(p, "Как настроить голос?", over, portrait.ContextRelevant)
	if !strings.Contains(c.Text, "Разработчик") || !strings.Contains(c.Text, "Отвечать коротко") || !strings.Contains(c.Text, "голос окна") || strings.Contains(c.Text, "Переезд") {
		t.Fatalf("только нужное: %q", c.Text)
	}
	// «Только нужное» тоже обязано уложиться в порог (ревью #482): лишнее —
	// с конца, «кто я» остается первым.
	c = portrait.Context(p, "Как настроить голос?", 25, portrait.ContextRelevant)
	if n := len([]rune(c.Text)); n > 25 || !strings.Contains(c.Text, "Разработчик") {
		t.Fatalf("только нужное в 25 знаков: %d %q", n, c.Text)
	}
}

// raceStore: первое сохранение проигрывает другому устройству — тот успел
// записать свой факт; хранилище отвечает ErrConflict, как бакет на If-Match.
type raceStore struct {
	memStore
	other domain.Fact
	lost  bool
}

func (r *raceStore) Save(ctx context.Context, p domain.Portrait) error {
	if !r.lost {
		r.lost = true
		r.p, _ = r.p.Add(r.other)
		return portrait.ErrConflict
	}
	return r.memStore.Save(ctx, p)
}

// Портрет на kb-сервере правят два устройства: проигравший перечитывает
// и применяет свою правку к свежему, ничего чужого не теряя.
func TestService_ConflictRereads(t *testing.T) {
	other, _ := domain.NewFact("o1", domain.SectionMisc, "Слушает ответы в машине", domain.OriginManual, domain.FactApproved, now)
	for _, tc := range []struct {
		name string
		do   func(*portrait.Service) error
		want string
	}{
		{"вручную", func(s *portrait.Service) error {
			_, err := s.Add(t.Context(), domain.SectionWork, "Пишет на Go")
			return err
		}, "Пишет на Go"},
		{"разбор ответа", func(s *portrait.Service) error {
			_, err := s.Extract(t.Context(), "вопрос", "ответ")
			return err
		}, "Отвечать коротко"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &raceStore{other: other}
			m := &fakeModel{reply: `[{"section":"prefs","text":"Отвечать коротко"}]`}
			n := 0
			s := portrait.NewService(st, m, func() time.Time { return now }, func() string { n++; return "f" + string(rune('0'+n)) })
			if err := tc.do(s); err != nil {
				t.Fatal(err)
			}
			got := portrait.Text(st.p.Facts())
			if !strings.Contains(got, "Слушает ответы в машине") || !strings.Contains(got, tc.want) {
				t.Fatalf("портрет после конфликта = %q", got)
			}
		})
	}
}

// Предложение модели в чате (инструмент «запомнить») ждет одобрения, помнит,
// откуда взялось, и не повторяет известное.
func TestService_Propose(t *testing.T) {
	ctx := t.Context()
	s, st := newSvc(t, &fakeModel{})
	f, err := s.Propose(ctx, sect(t, "prefs"), "Отвечать коротко")
	if err != nil || f.Approved() || f.Origin() != domain.OriginChat {
		t.Fatalf("предложено: %+v %v", f, err)
	}
	if len(st.p.Pending()) != 1 {
		t.Fatalf("ждут одобрения: %v", st.p.Pending())
	}
	if _, err := s.Propose(ctx, sect(t, "prefs"), "Отвечать коротко"); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("повтор = %v", err)
	}
}

// «Вернуть» после «Забыть» возвращает тот же факт: id, источник, дату и
// одобрение (ревью #482), а не новый «добавлено вручную».
func TestService_Restore(t *testing.T) {
	ctx := t.Context()
	s, st := newSvc(t, &fakeModel{})
	at := now.Add(-48 * time.Hour)
	f, _ := domain.NewFact("x7", sect(t, "prefs"), "Отвечать коротко", domain.OriginChat, domain.FactApproved, at)
	st.p, _ = st.p.Add(f)
	if err := s.Remove(ctx, "x7"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(ctx, f); err != nil {
		t.Fatal(err)
	}
	got := st.p.Facts()
	if len(got) != 1 || got[0].ID() != "x7" || got[0].Origin() != domain.OriginChat || !got[0].At().Equal(at) || !got[0].Approved() {
		t.Fatalf("вернулось: %+v", got)
	}
	if err := s.Restore(ctx, f); !errors.Is(err, domain.ErrFactExists) {
		t.Fatalf("второй возврат = %v", err)
	}
}

// Разбор после ответа не шлет модели больше предела (ревью #482): большой
// портрет и длинный ответ иначе переполняют маленькую модель на Маке.
func TestService_ExtractBounded(t *testing.T) {
	ctx := t.Context()
	m := &fakeModel{reply: "[]"}
	s, _ := newSvc(t, m)
	for i := range 60 {
		if _, err := s.Add(ctx, sect(t, "misc"), fmt.Sprintf("Длинный факт номер %d: %s", i, strings.Repeat("слово ", 40))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Extract(ctx, "вопрос", strings.Repeat("ответ ", 3000)); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(m.user)); n > portrait.MaxExtractRunes {
		t.Fatalf("в разбор ушло %d знаков, предел %d", n, portrait.MaxExtractRunes)
	}
}

// Текст портрета для модели — только одобренное, по разделам в их порядке.
// Названия разделов здесь — часть запроса к модели, а не подписи окна.
func TestText(t *testing.T) {
	mk := func(id, s, text string, st domain.FactStatus) domain.Fact {
		f, err := domain.NewFact(id, sect(t, s), text, domain.OriginManual, st, now)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	p, _ := domain.NewPortrait([]domain.Fact{
		mk("a", "prefs", "Отвечать коротко", domain.FactApproved),
		mk("b", "about", "Разработчик", domain.FactApproved),
		mk("c", "about", "Готовится к роли тимлида", domain.FactApproved),
		mk("d", "work", "Тайное, ждет одобрения", domain.FactPending),
	})
	want := "Обо мне: Разработчик; Готовится к роли тимлида.\nПредпочтения: Отвечать коротко."
	if got := portrait.Text(p.Approved()); got != want {
		t.Fatalf("текст:\n%s\nwant:\n%s", got, want)
	}
	if portrait.Text(nil) != "" {
		t.Fatal("пустой портрет не пуст")
	}
}

// У каждого раздела есть название для модели: раздел без него ушел бы в
// запрос как «: текст.».
func TestText_namesEverySection(t *testing.T) {
	for _, sec := range domain.FactSections() {
		f, _ := domain.NewFact("x", sec, "факт", domain.OriginManual, domain.FactApproved, now)
		if got := portrait.Text([]domain.Fact{f}); strings.HasPrefix(got, ":") {
			t.Errorf("раздел %q без названия: %q", sec, got)
		}
	}
}

// countStore считает чтения; changed — что «другое устройство» положит в
// портрет между первым и вторым чтением (пока думает модель).
type countStore struct {
	memStore
	loads     int
	versioned bool
	changed   domain.Fact
}

func (c *countStore) Load(ctx context.Context) (domain.Portrait, error) {
	c.loads++
	if c.loads == 2 && c.changed.ID() != "" {
		c.p, _ = c.p.Add(c.changed)
	}
	return c.memStore.Load(ctx)
}

// versionedStore — как kb-сервер: Save отказывает, если портрет сменился после Load.
type versionedStore struct{ *countStore }

func (versionedStore) ConflictChecked() {}

// Разбор ответа на kb-сервере читает портрет один раз (ревью #482: два GET на
// ответ); в папке, где версий нет, — перечитывает перед записью, чтобы не
// затереть то, что другой Мак успел записать, пока думала модель.
func TestService_ExtractLoads(t *testing.T) {
	other, _ := domain.NewFact("o1", domain.SectionMisc, "Слушает ответы в машине", domain.OriginManual, domain.FactApproved, now)
	for _, tc := range []struct {
		name      string
		versioned bool
		loads     int
	}{
		{"kb-сервер", true, 1},
		{"папка", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := &countStore{changed: other}
			var st portrait.Store = cs
			if tc.versioned {
				st, cs.changed = versionedStore{cs}, domain.Fact{}
			}
			m := &fakeModel{reply: `[{"section":"prefs","text":"Отвечать коротко"}]`}
			s := portrait.NewService(st, m, func() time.Time { return now }, func() string { return "n1" })
			if _, err := s.Extract(t.Context(), "вопрос", "ответ"); err != nil {
				t.Fatal(err)
			}
			if cs.loads != tc.loads {
				t.Fatalf("чтений %d, ждали %d", cs.loads, tc.loads)
			}
			if !tc.versioned && !strings.Contains(portrait.Text(cs.p.Facts()), "Слушает ответы в машине") {
				t.Fatalf("затерто записанное другим Маком: %q", portrait.Text(cs.p.Facts()))
			}
		})
	}
}

type versionedRace struct{ *raceStore }

func (versionedRace) ConflictChecked() {}

// На kb-сервере разбор пишет прочитанное до модели; другое устройство успело
// записать — Save отказывает, и сервис перечитывает, ничего не теряя.
func TestService_ExtractConflictOnServer(t *testing.T) {
	other, _ := domain.NewFact("o1", domain.SectionMisc, "Слушает ответы в машине", domain.OriginManual, domain.FactApproved, now)
	rs := &raceStore{other: other}
	m := &fakeModel{reply: `[{"section":"prefs","text":"Отвечать коротко"}]`}
	s := portrait.NewService(versionedRace{rs}, m, func() time.Time { return now }, func() string { return "n1" })
	if _, err := s.Extract(t.Context(), "вопрос", "ответ"); err != nil {
		t.Fatal(err)
	}
	got := portrait.Text(rs.p.Facts())
	if !strings.Contains(got, "Слушает ответы в машине") || !strings.Contains(got, "Отвечать коротко") {
		t.Fatalf("после конфликта = %q", got)
	}
}
