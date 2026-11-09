package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

type fakeStore struct {
	snap   memory.Snapshot
	added  []domain.ColdNote
	inited bool
}

func (f *fakeStore) Init() error                           { f.inited = true; return nil }
func (f *fakeStore) Load() (memory.Snapshot, error)        { return f.snap, nil }
func (f *fakeStore) Add(n domain.ColdNote, _ string) error { f.added = append(f.added, n); return nil }

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
	err := memory.New(st, memory.Limits{}).Add("Bad Name", "финансы", "x", "body")
	if !errors.Is(err, domain.ErrInvalidNote) || len(st.added) != 0 {
		t.Fatalf("err=%v added=%v", err, st.added)
	}
	if err := memory.New(st, memory.Limits{}).Add("good-name", "финансы", "про это", "body"); err != nil || len(st.added) != 1 {
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
