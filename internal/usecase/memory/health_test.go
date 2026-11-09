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
