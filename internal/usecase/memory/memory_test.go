package memory_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

type fakeStore struct {
	snap     memory.Snapshot
	added    []domain.ColdNote
	inited   bool
	savedHot string
	pending  []memory.PendingNote
	promoted []string
	archived map[string]string // имя в очереди -> имя в журнале

	texts      []memory.NoteText
	vectors    memory.VectorIndex
	hasVectors bool
}

func (f *fakeStore) ColdTexts() ([]memory.NoteText, error) { return f.texts, nil }
func (f *fakeStore) LoadVectors() (memory.VectorIndex, error) {
	if !f.hasVectors {
		return memory.VectorIndex{}, memory.ErrNoVectors
	}
	return f.vectors, nil
}
func (f *fakeStore) SaveVectors(v memory.VectorIndex) error {
	f.vectors, f.hasVectors = v, true
	return nil
}

func (f *fakeStore) AddPending(n domain.ColdNote, body string) error {
	f.pending = append(f.pending, memory.PendingNote{Note: n, Body: body})
	return nil
}
func (f *fakeStore) Pending() ([]memory.PendingNote, error) { return f.pending, nil }
func (f *fakeStore) ReadPending(name string) (memory.PendingNote, error) {
	for _, p := range f.pending {
		if p.Note.Name == name {
			return p, nil
		}
	}
	return memory.PendingNote{}, domain.ErrNoSuchPending
}
func (f *fakeStore) PromotePending(name string) error {
	p, err := f.ReadPending(name)
	if err != nil {
		return err
	}
	f.promoted = append(f.promoted, name)
	f.snap.Notes = append(f.snap.Notes, p.Note)
	return nil
}
func (f *fakeStore) ArchivePending(name, as string) error {
	if _, err := f.ReadPending(name); err != nil {
		return err
	}
	if f.archived == nil {
		f.archived = map[string]string{}
	}
	f.archived[name] = as
	return nil
}

func (f *fakeStore) SaveHot(hot string) error { f.savedHot = hot; f.snap.Hot = hot; return nil }

func (f *fakeStore) Init() error                    { f.inited = true; return nil }
func (f *fakeStore) Load() (memory.Snapshot, error) { return f.snap, nil }
func (f *fakeStore) Add(n domain.ColdNote, _ string) error {
	f.added = append(f.added, n)
	f.snap.Notes = append(f.snap.Notes, n)
	return nil
}

func TestService_CheckUsesTheLimitGiven(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: strings.Repeat("я", 50)}}
	ps, err := memory.New(st, memory.Limits{Hot: 10}).Check()
	if err != nil || len(ps) != 1 || ps[0].Kind != domain.ProblemHotTooBig {
		t.Fatalf("limit 10: %v, %v", ps, err)
	}
	ps, _ = memory.New(st, memory.Limits{}).Check() // 0 = лимит по умолчанию, 4000
	if len(ps) != 0 {
		t.Errorf("limit 0 должен брать DefaultHotLimit, got %v", ps)
	}
}

func TestService_AddValidatesBeforeTouchingTheStore(t *testing.T) {
	st := &fakeStore{}
	_, err := memory.New(st, memory.Limits{}).Add("Bad Name", "финансы", "x", "body")
	if !errors.Is(err, domain.ErrInvalidNote) || len(st.added) != 0 {
		t.Fatalf("err=%v added=%v", err, st.added)
	}
	if _, err := memory.New(st, memory.Limits{}).Add("good-name", "финансы", "про это", "body"); err != nil || len(st.added) != 1 {
		t.Fatalf("err=%v added=%v", err, st.added)
	}
}

func TestService_CheckUsesTheIndexLimitGiven(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "x", IndexRunes: 50}}
	ps, _ := memory.New(st, memory.Limits{Index: 10}).Check()
	if len(ps) != 1 || ps[0].Kind != domain.ProblemIndexTooBig {
		t.Fatalf("index limit 10: %v", ps)
	}
	if ps, _ := memory.New(st, memory.Limits{}).Check(); len(ps) != 0 {
		t.Errorf("0 должен брать DefaultIndexLimit, got %v", ps)
	}
}

func TestService_InitDelegates(t *testing.T) {
	st := &fakeStore{}
	if err := memory.New(st, memory.Limits{}).Init(); err != nil || !st.inited {
		t.Fatalf("err=%v inited=%v", err, st.inited)
	}
}

const markers = domain.TopicsStart + "\n" + domain.TopicsEnd

func TestService_TopicsRewritesTheBlock(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{
		Hot:   "правила\n" + markers + "\n",
		Notes: []domain.ColdNote{{File: "a.md", Name: "a", Topic: "финансы"}},
	}}
	changed, err := memory.New(st, memory.Limits{}).Topics()
	if err != nil || !changed || !strings.Contains(st.savedHot, "- финансы") {
		t.Fatalf("changed=%v err=%v hot=%q", changed, err, st.savedHot)
	}
	st.savedHot = ""
	changed, err = memory.New(st, memory.Limits{}).Topics()
	if err != nil || changed || st.savedHot != "" {
		t.Errorf("второй вызов ничего не меняет: changed=%v err=%v saved=%q", changed, err, st.savedHot)
	}
}

func TestService_AddRefreshesTopics(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: markers}}
	refreshed, err := memory.New(st, memory.Limits{}).Add("good-name", "здоровье", "про это", "body")
	if err != nil || !refreshed || len(st.added) != 1 {
		t.Fatalf("refreshed=%v err=%v added=%v", refreshed, err, st.added)
	}
}

// Запись сохранена, а HOT.md без меток человек правил сам: add не падает и
// не трогает его, а честно говорит, что перечень не обновлен.
func TestService_AddWithoutMarkersKeepsTheNote(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "ручной горячий слой"}}
	refreshed, err := memory.New(st, memory.Limits{}).Add("good-name", "здоровье", "про это", "body")
	if err != nil || refreshed || len(st.added) != 1 || st.savedHot != "" {
		t.Fatalf("refreshed=%v err=%v added=%v saved=%q", refreshed, err, st.added, st.savedHot)
	}
}

func at(t *testing.T) func() time.Time {
	t.Helper()
	return func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
}

const rulesHot = "## Правила работы\n\n-\n\n" + markers + "\n"

// Агент пишет только в очередь: ни холодный слой, ни горячий, ни перечень тем не меняются.
func TestService_ProposeGoesToQueueOnly(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	if err := memory.New(st, memory.Limits{}).Propose("agent-note", "стиль", "про это", "body"); err != nil {
		t.Fatal(err)
	}
	if len(st.pending) != 1 || st.pending[0].Note.Origin != domain.OriginAgent {
		t.Fatalf("pending = %+v", st.pending)
	}
	if len(st.added) != 0 || st.savedHot != "" {
		t.Errorf("агент задел холодный/горячий слой: added=%v hot=%q", st.added, st.savedHot)
	}
	if err := memory.New(st, memory.Limits{}).Propose("Bad Name", "стиль", "x", "b"); !errors.Is(err, domain.ErrInvalidNote) {
		t.Errorf("плохое имя в очереди: %v", err)
	}
}

func TestService_ApproveToCold(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t))
	_ = svc.Propose("agent-note", "стиль", "про это", "body")
	if err := svc.Approve("agent-note", domain.LayerCold); err != nil {
		t.Fatal(err)
	}
	if len(st.promoted) != 1 || !strings.Contains(st.savedHot, "- стиль") {
		t.Errorf("promoted=%v hot=%q", st.promoted, st.savedHot)
	}
}

func TestService_ApproveToHot(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t))
	_ = svc.Propose("agent-rule", "стиль", "не пушить в main", "body")
	if err := svc.Approve("agent-rule", domain.LayerHot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.savedHot, "- не пушить в main") {
		t.Errorf("правило не в горячем слое: %q", st.savedHot)
	}
	if st.archived["agent-rule"] != "2026-11-09-approved-agent-rule.md" || len(st.promoted) != 0 {
		t.Errorf("archived=%v promoted=%v", st.archived, st.promoted)
	}
}

// Потолок горячего слоя не обходится одобрением: правило остается в очереди.
func TestService_ApproveToHotRefusesOverTheLimit(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{Hot: len(rulesHot)}).WithClock(at(t))
	_ = svc.Propose("agent-rule", "стиль", "правило длиннее запаса", "body")
	if err := svc.Approve("agent-rule", domain.LayerHot); !errors.Is(err, domain.ErrHotFull) {
		t.Fatalf("err = %v, want ErrHotFull", err)
	}
	if st.savedHot != "" || len(st.archived) != 0 {
		t.Errorf("горячий слой или очередь тронуты: hot=%q archived=%v", st.savedHot, st.archived)
	}
}

func TestService_RejectArchivesInsteadOfDeleting(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t))
	_ = svc.Propose("agent-note", "стиль", "про это", "body")
	if err := svc.Reject("agent-note"); err != nil {
		t.Fatal(err)
	}
	if st.archived["agent-note"] != "2026-11-09-rejected-agent-note.md" {
		t.Errorf("archived = %v", st.archived)
	}
	if err := svc.Reject("nope"); !errors.Is(err, domain.ErrNoSuchPending) {
		t.Errorf("reject чужого: %v", err)
	}
}

type fakePortrait struct {
	text string
	err  error
}

func (f fakePortrait) Text() (string, error) { return f.text, f.err }

const whoHot = "## Кто я\n\n" + domain.PortraitStart + "\n" + domain.PortraitEnd + "\n"

func TestService_WhoWritesPortraitIntoHot(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: whoHot}}
	svc := memory.New(st, memory.Limits{}).WithPortrait(fakePortrait{text: "Обо мне: Разработчик."})
	changed, err := svc.Who()
	if err != nil || !changed || !strings.Contains(st.savedHot, "Обо мне: Разработчик.") {
		t.Fatalf("changed=%v err=%v hot=%q", changed, err, st.savedHot)
	}
	st.savedHot = ""
	if changed, err := svc.Who(); err != nil || changed || st.savedHot != "" {
		t.Errorf("второй вызов ничего не меняет: changed=%v err=%v saved=%q", changed, err, st.savedHot)
	}
}

func TestService_WhoRefusals(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "ручной горячий слой"}}
	svc := memory.New(st, memory.Limits{})
	if _, err := svc.Who(); !errors.Is(err, memory.ErrNoPortrait) {
		t.Errorf("без источника err = %v, want ErrNoPortrait", err)
	}
	svc = svc.WithPortrait(fakePortrait{text: "x"})
	if _, err := svc.Who(); !errors.Is(err, domain.ErrNoPortraitMarkers) || st.savedHot != "" {
		t.Errorf("без меток err = %v saved=%q", err, st.savedHot)
	}
	st.snap.Hot = whoHot
	svc = svc.WithPortrait(fakePortrait{err: errors.New("файл не читается")})
	if _, err := svc.Who(); err == nil || st.savedHot != "" {
		t.Errorf("ошибка источника должна всплыть и не стирать блок: %v saved=%q", err, st.savedHot)
	}
}
