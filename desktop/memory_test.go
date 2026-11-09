package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

func newMemDesk(t *testing.T) *memoryDesk {
	t.Helper()
	return &memoryDesk{dir: filepath.Join(t.TempDir(), "dvk-memory"), auth: &fakeAuth{ok: true}}
}

func (d *memoryDesk) seed(t *testing.T) *memory.Service {
	t.Helper()
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	return memory.New(memorydir.New(d.dir), memory.Limits{})
}

// Окно открывается и на человеке, у которого памяти еще нет: ничего не создается
// само, показывается приглашение завести.
func TestMemoryDesk_stateWithoutMemoryCreatesNothing(t *testing.T) {
	d := newMemDesk(t)
	v, err := d.State()
	if err != nil || v.Exists || v.Dir != d.dir {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err := os.Stat(d.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("State создал папку памяти: %v", err)
	}
}

func TestMemoryDesk_lifecycle(t *testing.T) {
	d := newMemDesk(t)
	svc := d.seed(t)
	if err := d.Init(); err == nil {
		t.Error("повторный Init затер бы память")
	}
	if _, err := svc.Add("fin-rules", "финансы", "про деньги", "тело"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Propose("agent-rule", "стиль", "не пушить в main", "обоснование"); err != nil {
		t.Fatal(err)
	}
	v, err := d.State()
	if err != nil || !v.Exists {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if len(v.Cold) != 1 || v.Cold[0].Name != "fin-rules" || v.Cold[0].Topic != "финансы" || !slices.Equal(v.Hot.Topics, []string{"финансы"}) {
		t.Errorf("cold=%+v topics=%v", v.Cold, v.Hot.Topics)
	}
	if len(v.Queue) != 1 || v.Queue[0].Body != "обоснование" || v.Queue[0].Origin != "agent" {
		t.Errorf("queue = %+v", v.Queue)
	}
	if v.Hot.Limit != domain.DefaultHotLimit || v.Hot.Runes == 0 || v.Hot.Tokens != v.Hot.Runes/3 || len(v.Problems) != 0 {
		t.Errorf("hot=%+v problems=%v", v.Hot, v.Problems)
	}

	if err := d.Approve("agent-rule", "hot"); err != nil {
		t.Fatal(err)
	}
	v, _ = d.State()
	if !strings.Contains(v.Hot.Text, "- не пушить в main") || len(v.Queue) != 0 || len(v.Journal) != 1 {
		t.Errorf("после approve hot: queue=%d journal=%v", len(v.Queue), v.Journal)
	}

	_ = svc.Propose("other", "стиль", "лишнее", "x")
	if err := d.Reject("other"); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.State(); len(v.Journal) != 2 || len(v.Queue) != 0 {
		t.Errorf("после reject: journal=%v queue=%d", v.Journal, len(v.Queue))
	}

	if err := d.Approve("nope", "cold"); !errors.Is(err, domain.ErrNoSuchPending) {
		t.Errorf("approve чужого: %v", err)
	}
	if err := d.Approve("other", "middle"); err == nil {
		t.Error("слой middle должен быть отвергнут")
	}
}

func TestMemoryDesk_approveToColdRefreshesTopics(t *testing.T) {
	d := newMemDesk(t)
	svc := d.seed(t)
	_ = svc.Propose("agent-note", "здоровье", "про врача", "x")
	if err := d.Approve("agent-note", "cold"); err != nil {
		t.Fatal(err)
	}
	v, _ := d.State()
	if !slices.Equal(v.Hot.Topics, []string{"здоровье"}) || len(v.Problems) != 0 {
		t.Errorf("topics=%v problems=%v", v.Hot.Topics, v.Problems)
	}
}

func TestMemoryDesk_hotLimit(t *testing.T) {
	d := newMemDesk(t)
	d.seed(t)
	if err := memorydir.New(d.dir).SaveLimits(memory.Limits{Index: 50000}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetHotLimit(6000); err != nil {
		t.Fatal(err)
	}
	lim, _ := memorydir.New(d.dir).Limits()
	if lim.Hot != 6000 || lim.Index != 50000 {
		t.Errorf("limits = %+v (потолок индекса не должен теряться)", lim)
	}
	if v, _ := d.State(); v.Hot.Limit != 6000 {
		t.Errorf("Hot.Limit = %d", v.Hot.Limit)
	}
	if err := d.SetHotLimit(5); !errors.Is(err, domain.ErrInvalidLimits) {
		t.Errorf("негодный потолок: %v", err)
	}
	if lim, _ := memorydir.New(d.dir).Limits(); lim.Hot != 6000 {
		t.Error("отказ затер прежний потолок")
	}
}

func TestMemoryDesk_problemsSurface(t *testing.T) {
	d := newMemDesk(t)
	d.seed(t)
	if err := os.WriteFile(filepath.Join(d.dir, "HOT.md"), []byte(strings.Repeat("я", 5000)), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := d.State()
	if err != nil || len(v.Problems) != 1 || v.Problems[0].Kind != "hot-too-big" || v.Problems[0].File != "HOT.md" {
		t.Fatalf("problems=%+v err=%v", v.Problems, err)
	}
}

func TestMemoryDesk_topicsFixStaleList(t *testing.T) {
	d := newMemDesk(t)
	d.seed(t)
	hand := "---\nname: b\ntopic: здоровье\norigin: human\ndescription: про b\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(d.dir, "cold", "b.md"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(d.dir, "cold", "INDEX.md")
	cur, _ := os.ReadFile(idx)
	if err := os.WriteFile(idx, append(cur, []byte("- [b](b.md) — про b\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := d.Topics()
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if v, _ := d.State(); len(v.Problems) != 0 || !slices.Equal(v.Hot.Topics, []string{"здоровье"}) {
		t.Errorf("problems=%v topics=%v", v.Problems, v.Hot.Topics)
	}
}

type textSource string

func (s textSource) Text() (string, error) { return string(s), nil }

func TestMemoryDesk_whoFillsThePortraitBlock(t *testing.T) {
	d := newMemDesk(t)
	d.seed(t)
	changed, err := d.Who(textSource("Обо мне: Разработчик."))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if v, _ := d.State(); !strings.Contains(v.Hot.Text, "Обо мне: Разработчик.") {
		t.Errorf("блок не заполнен:\n%s", v.Hot.Text)
	}
}

// Правило в горячем слое начинает действовать во всех сессиях агентов сразу,
// поэтому одобрение туда идет только после Touch ID, каждый раз; в холодный слой
// и отклонение Touch ID не просят.
func TestMemoryDesk_approveToHotAsksTouchID(t *testing.T) {
	d := newMemDesk(t)
	auth := d.auth.(*fakeAuth)
	svc := d.seed(t)
	_ = svc.Propose("rule-a", "стиль", "не пушить в main", "x")
	_ = svc.Propose("note-b", "стиль", "факт", "x")

	auth.ok = false
	if err := d.Approve("rule-a", "hot"); !errors.Is(err, errAuthDenied) {
		t.Fatalf("отказ Touch ID: %v", err)
	}
	v, _ := d.State()
	if strings.Contains(v.Hot.Text, "не пушить") || len(v.Queue) != 2 {
		t.Errorf("после отказа правило попало в слой или ушло из очереди: queue=%d", len(v.Queue))
	}

	auth.ok = true
	if err := d.Approve("rule-a", "hot"); err != nil {
		t.Fatal(err)
	}
	if len(auth.asked) != 2 || !strings.Contains(auth.asked[1], "горяч") {
		t.Errorf("asked = %v", auth.asked)
	}

	n := len(auth.asked)
	if err := d.Approve("note-b", "cold"); err != nil || len(auth.asked) != n {
		t.Errorf("холодный слой не должен просить Touch ID: err=%v asked=%v", err, auth.asked)
	}
}

func TestMemoryDesk_approveToHotWithoutHelperRefuses(t *testing.T) {
	d := newMemDesk(t)
	d.auth = nil
	_ = d.seed(t).Propose("rule-a", "стиль", "правило", "x")
	if err := d.Approve("rule-a", "hot"); err == nil {
		t.Error("без помощника kb-auth правило не должно пройти в горячий слой")
	}
	if v, _ := d.State(); len(v.Queue) != 1 {
		t.Error("очередь изменилась")
	}
}
