package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
	"github.com/daniil/kb-engine/internal/usecase/search"
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
	if err := memorydir.New(d.dir).Seal(); err != nil { // правка руками подтверждена
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

	// Холодный слой тоже переписывает HOT.md (перечень тем): Touch ID и там.
	n := len(auth.asked)
	if err := d.Approve("note-b", "cold"); err != nil || len(auth.asked) != n+1 {
		t.Errorf("холодный слой должен просить Touch ID: err=%v asked=%v", err, auth.asked)
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

// Любая запись в HOT.md из окна — по Touch ID: правила, перечень тем, «Кто я»,
// подтверждение печати. Отклонить и потолок HOT.md не меняют.
func TestMemoryDesk_everyHotWriteAsksTouchID(t *testing.T) {
	d := newMemDesk(t)
	auth := d.auth.(*fakeAuth)
	svc := d.seed(t)
	auth.ok = false
	if _, err := d.Topics(); !errors.Is(err, errAuthDenied) {
		t.Errorf("Topics: %v", err)
	}
	if _, err := d.Who(textSource("Обо мне: x.")); !errors.Is(err, errAuthDenied) {
		t.Errorf("Who: %v", err)
	}
	if err := d.Seal(); !errors.Is(err, errAuthDenied) {
		t.Errorf("Seal: %v", err)
	}
	_ = svc.Propose("junk", "стиль", "лишнее", "x")
	n := len(auth.asked)
	if err := d.Reject("junk"); err != nil || len(auth.asked) != n {
		t.Errorf("Reject не пишет в HOT.md и Touch ID не просит: %v", err)
	}
	if err := d.SetHotLimit(6000); err != nil || len(auth.asked) != n {
		t.Errorf("потолок не меняет содержимое: %v", err)
	}
}

func TestMemoryDesk_stateShowsDecisionsAndSeal(t *testing.T) {
	d := newMemDesk(t)
	svc := d.seed(t)
	_ = svc.Propose("rule-a", "стиль", "не пушить в main", "x")
	if err := d.Approve("rule-a", "hot"); err != nil {
		t.Fatal(err)
	}
	v, _ := d.State()
	if len(v.Decisions) != 1 || !strings.Contains(v.Decisions[0], "rule-a") {
		t.Errorf("decisions = %v", v.Decisions)
	}
	if err := os.WriteFile(filepath.Join(d.dir, "HOT.md"), []byte("подсунули"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, _ = d.State()
	found := false
	for _, p := range v.Problems {
		found = found || p.Kind == "hot-changed-outside"
	}
	if !found {
		t.Errorf("правка мимо системы не видна: %+v", v.Problems)
	}
	if err := d.Seal(); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.State(); len(v.Problems) != 1 && len(v.Problems) != 0 { // «подсунули» — не наш формат: допустимы другие нарушения, но не печать
		t.Logf("problems: %+v", v.Problems)
	}
	for _, p := range v.Problems {
		if p.Kind == "hot-changed-outside" {
			t.Error("после Seal печать должна совпасть")
		}
	}
}

// Экран «Здоровье»: счёт, режим, правила с возрастом и события для ритма приходят
// вместе с остальным состоянием, одним чтением папки.
func TestMemoryDesk_stateCarriesHealth(t *testing.T) {
	d := newMemDesk(t)
	svc := d.seed(t)
	v, _ := d.State()
	if v.Health.Mode != "ok" || v.Health.Total != 100 || len(v.Health.Parts) != 4 {
		t.Fatalf("свежая память: %+v", v.Health)
	}
	_ = svc.Propose("rule-a", "стиль", "не пушить в main", "x")
	if err := d.Approve("rule-a", "hot"); err != nil {
		t.Fatal(err)
	}
	v, _ = d.State()
	h := v.Health
	if len(h.Rules) != 1 || h.Rules[0].Text != "не пушить в main" || h.Rules[0].AgeDays != 0 || h.Rules[0].Stale {
		t.Errorf("rules = %+v", h.Rules)
	}
	if len(h.Events) != 1 || h.Events[0].Kind != "approve-hot" || h.Events[0].Text != "не пушить в main" {
		t.Errorf("events = %+v", h.Events)
	}
	if h.Today == "" || h.Events[0].Day != h.Today || h.IdleDays != 0 {
		t.Errorf("today=%q day=%q idle=%d", h.Today, h.Events[0].Day, h.IdleDays)
	}
}

func TestMemoryDesk_queueShowsHowLongItWaits(t *testing.T) {
	d := newMemDesk(t)
	svc := d.seed(t)
	_ = svc.Propose("old-one", "стиль", "давно ждет", "x")
	old := time.Now().AddDate(0, 0, -12)
	if err := os.Chtimes(filepath.Join(d.dir, "pending", "old-one.md"), old, old); err != nil {
		t.Fatal(err)
	}
	v, _ := d.State()
	if len(v.Queue) != 1 || v.Queue[0].WaitDays != 12 || v.Health.OldestPendingDays != 12 {
		t.Fatalf("queue=%+v health.oldest=%d", v.Queue, v.Health.OldestPendingDays)
	}
}

// Осмотр и подтверждение правила поднимают доверие, поэтому идут по Touch ID, как
// запись в горячий слой; отказ ничего не пишет в журнал.
func TestMemoryDesk_reviewAndConfirmAskTouchID(t *testing.T) {
	d := newMemDesk(t)
	auth := d.auth.(*fakeAuth)
	svc := d.seed(t)
	_ = svc.Propose("rule-a", "стиль", "не пушить в main", "x")
	if err := d.Approve("rule-a", "hot"); err != nil {
		t.Fatal(err)
	}
	decisions := func() int { v, _ := d.State(); return len(v.Decisions) }
	before := decisions()
	auth.ok = false
	if err := d.Review(); !errors.Is(err, errAuthDenied) {
		t.Errorf("Review: %v", err)
	}
	if err := d.ConfirmRule("не пушить в main"); !errors.Is(err, errAuthDenied) {
		t.Errorf("ConfirmRule: %v", err)
	}
	if decisions() != before {
		t.Error("отказ Touch ID оставил запись в журнале")
	}
	auth.ok = true
	if err := d.Review(); err != nil {
		t.Fatal(err)
	}
	if err := d.ConfirmRule("Не пушить в main."); err != nil {
		t.Fatal(err)
	}
	if err := d.ConfirmRule("выдуманное"); !errors.Is(err, domain.ErrNoSuchRule) {
		t.Errorf("выдуманное правило: %v", err)
	}
	if decisions() != before+2 {
		t.Errorf("в журнале %d, want %d", decisions(), before+2)
	}
}

// Подтвердить все устаревшие правила одним жестом: Touch ID один раз, запись по каждому.
func TestMemoryDesk_confirmRulesAsksTouchIDOnce(t *testing.T) {
	d := newMemDesk(t)
	auth := d.auth.(*fakeAuth)
	svc := d.seed(t)
	_ = svc.Propose("r-a", "стиль", "не пушить в main", "x")
	_ = svc.Propose("r-b", "стиль", "релиз в пятницу не катим", "x")
	for _, n := range []string{"r-a", "r-b"} {
		if err := d.Approve(n, "hot"); err != nil {
			t.Fatal(err)
		}
	}
	auth.asked = nil
	auth.ok = false
	if err := d.ConfirmRules([]string{"не пушить в main", "релиз в пятницу не катим"}); !errors.Is(err, errAuthDenied) {
		t.Fatalf("без Touch ID: %v", err)
	}
	auth.ok = true
	before, _ := d.State()
	if err := d.ConfirmRules([]string{"не пушить в main", "Релиз в пятницу не катим."}); err != nil {
		t.Fatal(err)
	}
	after, _ := d.State()
	if len(auth.asked) != 2 || len(after.Decisions) != len(before.Decisions)+2 {
		t.Errorf("asked=%d decisions %d -> %d", len(auth.asked), len(before.Decisions), len(after.Decisions))
	}
}

type stubEmbedder struct{ err error }

func (s stubEmbedder) Embed(string) (search.Vector, error) {
	if s.err != nil {
		return nil, s.err
	}
	return search.Vector{1, 0, 0}, nil
}

// Кнопка «Пересобрать векторы»: без Touch ID (HOT.md не меняется), ошибка службы — словами.
func TestMemoryDesk_indexRebuildsVectors(t *testing.T) {
	d := newMemDesk(t)
	auth := d.auth.(*fakeAuth)
	svc := d.seed(t)
	d.embed = stubEmbedder{}
	if _, err := svc.Add("fin-rules", "финансы", "про деньги", "тело"); err != nil {
		t.Fatal(err)
	}
	rep, err := d.Index()
	if err != nil || rep.Embedded != 1 || len(auth.asked) != 0 {
		t.Fatalf("rep=%+v err=%v asked=%v", rep, err, auth.asked)
	}
	_, _ = svc.Add("second", "финансы", "еще", "тело")
	hasVectors := func() bool {
		v, _ := d.State()
		for _, r := range v.Health.Reasons {
			if r.Kind == "vectors" {
				return true
			}
		}
		return false
	}
	if !hasVectors() {
		t.Fatal("после новой записи векторы должны отстать")
	}
	if _, err := d.Index(); err != nil {
		t.Fatal(err)
	}
	if hasVectors() {
		t.Error("после пересборки векторы свежие")
	}
	d.embed = stubEmbedder{err: errors.New("служба молчит")}
	if _, err := d.Index(); err == nil {
		t.Error("недоступная служба эмбеддингов должна вернуть ошибку")
	}
}
