package memory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

const healthHot = "# HOT\n\n## Правила работы\n\n- Цену называть от нижней границы\n- Релиз в пятницу не катим\n"

func healthService(st *fakeStore) *memory.Service {
	return memory.New(st, memory.Limits{}).WithClock(func() time.Time {
		return time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	})
}

func TestService_HealthReadsWhatTheSystemAlreadyWrites(t *testing.T) {
	st := &fakeStore{
		snap: memory.Snapshot{Hot: healthHot},
		decisions: []string{
			"2026-10-01 10:00 одобрено в горячий слой: a — «Цену называть от нижней границы»",
			"2026-11-05 10:00 правило подтверждено: Релиз в пятницу не катим",
			"2026-11-06 10:00 перечень тем в HOT.md пересобран из тем записей",
			"строка без даты",
		},
		pending: []memory.PendingNote{{Note: domain.ColdNote{Name: "agent-vpn"}, Since: time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)}},
	}
	st.sealed = domain.HotDigest(healthHot)
	h, err := healthService(st).Health()
	if err != nil {
		t.Fatal(err)
	}
	if h.IdleDays != 5 {
		t.Errorf("IdleDays = %d, want 5 (обслуживание 06.11 не считается)", h.IdleDays)
	}
	if len(h.Rules) != 2 || h.Rules[0].AgeDays != 40 || !h.Rules[0].Stale || h.Rules[1].AgeDays != 5 {
		t.Errorf("rules = %+v", h.Rules)
	}
	if h.OldestPendingDays != 9 {
		t.Errorf("OldestPendingDays = %d, want 9", h.OldestPendingDays)
	}
	if h.Mode != domain.TrustOK {
		t.Errorf("mode = %s, total %d", h.Mode, h.Total)
	}
}

func TestService_HealthSeesAnEditMadeOutsideTheSystem(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: healthHot}, sealed: "другой-отпечаток"}
	h, err := healthService(st).Health()
	if err != nil || h.Mode != domain.TrustBad {
		t.Fatalf("mode = %s, err = %v", h.Mode, err)
	}
}

func TestService_ReviewAndConfirmRuleAreLogged(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: healthHot}}
	svc := healthService(st)
	if err := svc.Review(); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmRule("  релиз в ПЯТНИЦУ не катим. "); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2026-11-10 12:00 осмотр: без изменений",
		"2026-11-10 12:00 правило подтверждено: Релиз в пятницу не катим", // текст из HOT.md, а не как набрали
	}
	if len(st.decisions) != 2 || st.decisions[0] != want[0] || st.decisions[1] != want[1] {
		t.Fatalf("decisions = %q", st.decisions)
	}
	h, _ := svc.Health()
	if h.Rules[1].AgeDays != 0 || h.IdleDays != 0 {
		t.Errorf("после подтверждения возраст правила 0: %+v idle=%d", h.Rules[1], h.IdleDays)
	}
}

func TestService_ConfirmRuleRefusesARuleThatIsNotInHot(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: healthHot}}
	err := healthService(st).ConfirmRule("выдуманное правило")
	if !errors.Is(err, domain.ErrNoSuchRule) || len(st.decisions) != 0 {
		t.Fatalf("err = %v, decisions = %q", err, st.decisions)
	}
}

func TestService_OverviewCarriesHealthAndEvents(t *testing.T) {
	st := &fakeStore{
		snap:      memory.Snapshot{Hot: healthHot},
		decisions: []string{"2026-11-05 10:00 осмотр: без изменений"},
	}
	ov, err := healthService(st).Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Events) != 1 || ov.Events[0].Kind != domain.DecisionReview || ov.Health.IdleDays != 5 {
		t.Fatalf("events = %+v health.idle = %d", ov.Events, ov.Health.IdleDays)
	}
}

func TestService_HealthMissingDirectoryDecisionsIsNotAnError(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: healthHot}}
	h, err := healthService(st).Health()
	if err != nil || h.IdleDays != -1 {
		t.Fatalf("idle = %d, err = %v", h.IdleDays, err)
	}
}

// Здоровье читает журнал по формулировкам сценария: если одна поменяется без другой,
// решение станет «неизвестным» и перестанет считаться осмотром. Тест держит их вместе.
func TestService_EveryLoggedDecisionIsRecognisedByHealth(t *testing.T) {
	st := &fakeStore{
		snap: memory.Snapshot{Hot: healthHot + "\n<!-- topics:start -->\n<!-- topics:end -->\n<!-- portrait:start -->\n<!-- portrait:end -->\n"},
		pending: []memory.PendingNote{
			{Note: domain.ColdNote{Name: "to-hot", Topic: "т", Description: "новое правило"}},
			{Note: domain.ColdNote{Name: "to-cold", Topic: "т", Description: "факт"}},
			{Note: domain.ColdNote{Name: "to-bin", Topic: "т", Description: "мусор"}},
		},
	}
	svc := healthService(st).WithPortrait(fixedPortrait("обо мне"))
	steps := []struct {
		name string
		do   func() error
		kind domain.DecisionKind
		text string
	}{
		{"hot", func() error { return svc.Approve("to-hot", domain.LayerHot) }, domain.DecisionApproveHot, "новое правило"},
		{"cold", func() error { return svc.Approve("to-cold", domain.LayerCold) }, domain.DecisionApproveCold, "to-cold"},
		{"reject", func() error { return svc.Reject("to-bin") }, domain.DecisionReject, "to-bin"},
		{"seal", svc.Seal, domain.DecisionSeal, ""},
		{"review", svc.Review, domain.DecisionReview, ""},
		{"confirm", func() error { return svc.ConfirmRule("Релиз в пятницу не катим") }, domain.DecisionConfirmRule, "Релиз в пятницу не катим"},
		{"topics", func() error {
			st.snap.Notes = append(st.snap.Notes, domain.ColdNote{Name: "x", Topic: "новая тема"})
			_, err := svc.Topics()
			return err
		}, domain.DecisionTopics, ""},
		{"portrait", func() error { _, err := svc.Who(); return err }, domain.DecisionPortrait, ""},
	}
	for i, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(st.decisions) != i+1 {
			t.Fatalf("%s: в журнале %d строк, want %d", s.name, len(st.decisions), i+1)
		}
		d, ok := domain.ParseDecision(st.decisions[i])
		if !ok || d.Kind != s.kind || d.Text != s.text {
			t.Errorf("%s: %q разобрано как %+v (ok=%v), want %s %q", s.name, st.decisions[i], d, ok, s.kind, s.text)
		}
	}
}

type fixedPortrait string

func (f fixedPortrait) Text() (string, error) { return string(f), nil }

// Подтвердить сразу несколько правил: все или ничего, чтобы выдуманное правило в
// списке не оставило в журнале половину отметок.
func TestService_ConfirmRulesIsAllOrNothing(t *testing.T) {
	st := &fakeStore{snap: memory.Snapshot{Hot: healthHot}}
	svc := healthService(st)
	if err := svc.ConfirmRules([]string{"Цену называть от нижней границы", "выдуманное"}); !errors.Is(err, domain.ErrNoSuchRule) || len(st.decisions) != 0 {
		t.Fatalf("err = %v, decisions = %q", err, st.decisions)
	}
	if err := svc.ConfirmRules([]string{"цену называть от нижней границы.", "Релиз в пятницу не катим"}); err != nil {
		t.Fatal(err)
	}
	if len(st.decisions) != 2 || st.decisions[0] != "2026-11-10 12:00 правило подтверждено: Цену называть от нижней границы" {
		t.Fatalf("decisions = %q", st.decisions)
	}
	if err := svc.ConfirmRules(nil); err != nil || len(st.decisions) != 2 {
		t.Errorf("пустой список — ничего не делает: %v %q", err, st.decisions)
	}
	h, _ := svc.Health()
	if h.StaleRules != 0 || h.Rules[0].AgeDays != 0 || h.Rules[1].AgeDays != 0 {
		t.Errorf("rules = %+v", h.Rules)
	}
}
