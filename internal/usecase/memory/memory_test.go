package memory_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	journal    []string
	loads      int
	decisions  []string
	sealed     string
	vectors    memory.VectorIndex
	hasVectors bool
}

func (f *fakeStore) AppendDecision(line string) error {
	f.decisions = append(f.decisions, line)
	return nil
}
func (f *fakeStore) Decisions(n int) ([]string, error) {
	if len(f.decisions) > n {
		return f.decisions[len(f.decisions)-n:], nil
	}
	return f.decisions, nil
}
func (f *fakeStore) Seal() error                   { f.sealed = domain.HotDigest(f.snap.Hot); return nil }
func (f *fakeStore) SealedDigest() (string, error) { return f.sealed, nil }

func (f *fakeStore) JournalNames() ([]string, error) { return f.journal, nil }

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

func (f *fakeStore) SaveHot(hot string) error {
	f.savedHot = hot
	f.snap.Hot = hot
	f.sealed = domain.HotDigest(hot) // как настоящее хранилище: своя запись запечатывается
	return nil
}

func (f *fakeStore) Init() error                    { f.inited = true; return nil }
func (f *fakeStore) Load() (memory.Snapshot, error) { f.loads++; return f.snap, nil }
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

func TestService_OverviewGathersEverythingTheWindowShows(t *testing.T) {
	st := &fakeStore{
		snap:    memory.Snapshot{Hot: strings.Repeat("я", 50), Notes: []domain.ColdNote{{File: "a.md", Name: "a", Topic: "t", Description: "d"}}},
		journal: []string{"2026-11-09-x.md"},
	}
	svc := memory.New(st, memory.Limits{Hot: 10})
	_ = svc.Propose("agent-note", "стиль", "про это", "body")
	ov, err := svc.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Snapshot.Notes) != 1 || len(ov.Pending) != 1 || len(ov.Journal) != 1 || ov.Limits.Hot != 10 {
		t.Errorf("overview = %+v", ov)
	}
	var kinds []domain.ProblemKind
	for _, p := range ov.Problems {
		kinds = append(kinds, p.Kind)
	}
	if !slices.Contains(kinds, domain.ProblemHotTooBig) {
		t.Errorf("проблемы не посчитаны: %v", kinds)
	}
}

// Запись в очереди могли положить мимо Propose: одобрять ее без проверки нельзя.
func TestService_ApproveRevalidatesTheQueuedNote(t *testing.T) {
	bad := memory.PendingNote{Note: domain.ColdNote{File: "evil.md", Name: "evil", Topic: "t", Description: "x " + domain.TopicsEnd}, Body: "b"}
	for _, to := range []domain.Layer{domain.LayerCold, domain.LayerHot} {
		st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}, pending: []memory.PendingNote{bad}}
		err := memory.New(st, memory.Limits{}).WithClock(at(t)).Approve("evil", to)
		if !errors.Is(err, domain.ErrInvalidNote) || len(st.promoted) != 0 || st.savedHot != "" || len(st.archived) != 0 {
			t.Errorf("%s: err=%v promoted=%v hot=%q archived=%v", to, err, st.promoted, st.savedHot, st.archived)
		}
	}
}

// То же правило второй раз не дописывается, а запись из очереди все равно уходит в журнал.
func TestService_ApproveToHotDoesNotDuplicateARule(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "## Правила работы\n- не пушить в main\n\n" + markers + "\n"}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t))
	_ = svc.Propose("again", "стиль", "не пушить в main", "x")
	if err := svc.Approve("again", domain.LayerHot); err != nil {
		t.Fatal(err)
	}
	if st.savedHot != "" || st.archived["again"] == "" {
		t.Errorf("savedHot=%q archived=%v", st.savedHot, st.archived)
	}
}

// «Кто я» упирается в тот же потолок, что и правила: проверка после записи ничего не вернет.
func TestService_WhoRefusesOverTheLimit(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: whoHot}}
	svc := memory.New(st, memory.Limits{Hot: len(whoHot) / 2}).WithPortrait(fakePortrait{text: strings.Repeat("факт ", 100)})
	if _, err := svc.Who(); !errors.Is(err, domain.ErrHotFull) || st.savedHot != "" {
		t.Errorf("err = %v saved=%q", err, st.savedHot)
	}
}

func TestService_OverviewLoadsTheSnapshotOnce(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "x"}}
	if _, err := memory.New(st, memory.Limits{}).Overview(); err != nil {
		t.Fatal(err)
	}
	if st.loads != 1 {
		t.Errorf("Load вызван %d раз, want 1", st.loads)
	}
}

// Каждое решение человека остается в журнале: что, когда и с каким текстом.
func TestService_DecisionsAreLogged(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t)).WithPortrait(fakePortrait{text: "Обо мне: Разработчик."})
	_ = svc.Propose("rule-a", "стиль", "не пушить в main", "x")
	_ = svc.Propose("note-b", "здоровье", "про врача", "x")
	_ = svc.Propose("junk", "стиль", "лишнее", "x")
	if err := svc.Approve("rule-a", domain.LayerHot); err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve("note-b", domain.LayerCold); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reject("junk"); err != nil {
		t.Fatal(err)
	}
	st.snap.Hot = whoHot // портрет еще не вписан
	if _, err := svc.Who(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(st.decisions, "\n")
	for _, want := range []string{"2026-11-09", "одобрено в горячий слой", "rule-a", "не пушить в main", "одобрено в холодный слой", "note-b", "отклонено", "junk", "«Кто я»"} {
		if !strings.Contains(joined, want) {
			t.Errorf("в журнале решений нет %q:\n%s", want, joined)
		}
	}
	for _, line := range st.decisions {
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("запись журнала должна быть одной строкой: %q", line)
		}
	}
}

func TestService_FailedActionLeavesNoDecision(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{Hot: len(rulesHot)}).WithClock(at(t))
	_ = svc.Propose("long", "стиль", "правило длиннее запаса для потолка", "x")
	if err := svc.Approve("long", domain.LayerHot); err == nil {
		t.Fatal("ожидался отказ по потолку")
	}
	if len(st.decisions) != 0 {
		t.Errorf("отказ записан как решение: %v", st.decisions)
	}
}

// Правка HOT.md мимо системы видна проверке, пока человек ее не подтвердит печатью.
func TestService_SealDetectsAndAcknowledgesOutsideEdits(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: rulesHot}}
	svc := memory.New(st, memory.Limits{}).WithClock(at(t))
	if err := svc.Seal(); err != nil {
		t.Fatal(err)
	}
	if ps, _ := svc.Check(); len(ps) != 0 {
		t.Fatalf("свежая печать: %v", ps)
	}
	st.snap.Hot += "- подсунутое правило\n" // кто-то правит файл мимо системы
	ps, _ := svc.Check()
	if len(ps) != 1 || ps[0].Kind != domain.ProblemHotChanged {
		t.Fatalf("ps = %v", ps)
	}
	if err := svc.Seal(); err != nil {
		t.Fatal(err)
	}
	if ps, _ := svc.Check(); len(ps) != 0 {
		t.Errorf("после подтверждения: %v", ps)
	}
	if len(st.decisions) != 2 || !strings.Contains(st.decisions[1], "подтверждено") {
		t.Errorf("подтверждение печати должно остаться в журнале: %v", st.decisions)
	}
}

func TestService_OverviewShowsRecentDecisions(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: "x"}}
	for i := range 30 {
		_ = st.AppendDecision(strings.Repeat("р", i+1))
	}
	ov, err := memory.New(st, memory.Limits{}).Overview()
	if err != nil || len(ov.Decisions) != 20 || utf8.RuneCountInString(ov.Decisions[19]) != 30 {
		t.Fatalf("decisions=%d err=%v", len(ov.Decisions), err)
	}
}
