package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

type fakeStore struct {
	snap     memory.Snapshot
	added    []domain.ColdNote
	inited   bool
	savedHot string
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
