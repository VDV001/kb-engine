package domain

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		ok       bool
		kind     DecisionKind
		text     string
		day      string
		judgment bool
	}{
		{"hot rule", "2026-11-01 10:05 одобрено в горячий слой: agent-style — «ответы владельцу короче»", true, DecisionApproveHot, "ответы владельцу короче", "2026-11-01", true},
		{"hot rule already there", "2026-11-01 10:05 одобрено в горячий слой: agent-style — «ответы короче» (такое правило уже было)", true, DecisionApproveDup, "ответы короче", "2026-11-01", true},
		{"cold", "2026-11-02 09:00 одобрено в холодный слой: vpn-notes (тема «сети»)", true, DecisionApproveCold, "vpn-notes", "2026-11-02", true},
		{"reject", "2026-11-03 09:00 отклонено: junk-note — «мусор»", true, DecisionReject, "junk-note", "2026-11-03", true},
		{"seal", "2026-11-04 09:00 подтверждено: текущий HOT.md принят как есть", true, DecisionSeal, "", "2026-11-04", true},
		{"review", "2026-11-05 09:00 осмотр: без изменений", true, DecisionReview, "", "2026-11-05", true},
		{"confirm rule", "2026-11-06 09:00 правило подтверждено: Не пушить в main напрямую", true, DecisionConfirmRule, "Не пушить в main напрямую", "2026-11-06", true},
		{"topics is upkeep", "2026-11-07 09:00 перечень тем в HOT.md пересобран из тем записей", true, DecisionTopics, "", "2026-11-07", false},
		{"portrait is upkeep", "2026-11-07 09:00 блок «Кто я» в HOT.md обновлен из одобренных фактов портрета", true, DecisionPortrait, "", "2026-11-07", false},
		{"index is upkeep", "2026-11-07 09:00 индекс векторов пересобран", true, DecisionIndex, "", "2026-11-07", false},
		{"unknown text keeps the date", "2026-11-08 09:00 что-то новое", true, DecisionOther, "что-то новое", "2026-11-08", false},
		{"no date", "одобрено в холодный слой: x", false, "", "", "", false},
		{"bad date", "2026-13-45 09:00 осмотр: x", false, "", "", "", false},
		{"empty", "", false, "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := ParseDecision(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if d.Kind != tt.kind || d.Text != tt.text || !d.Day.Equal(day(tt.day)) {
				t.Fatalf("got %+v, want kind=%s text=%q day=%s", d, tt.kind, tt.text, tt.day)
			}
			if d.Kind.IsJudgment() != tt.judgment {
				t.Fatalf("IsJudgment = %v, want %v", d.Kind.IsJudgment(), tt.judgment)
			}
		})
	}
}

func TestRulesOf(t *testing.T) {
	hot := "# HOT\n\n## Правила работы\n\n- Цену называть от нижней границы\n-\n- Релиз в пятницу не катим\n\n## Другое\n\n- не правило\n"
	got := RulesOf(hot)
	want := []string{"Цену называть от нижней границы", "Релиз в пятницу не катим"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RulesOf = %q, want %q", got, want)
	}
	if got := RulesOf("# HOT без раздела\n- x\n"); len(got) != 0 {
		t.Fatalf("без раздела правил список пуст, got %q", got)
	}
}

func TestRuleKey_ignoresCaseSpacesAndFinalDot(t *testing.T) {
	if RuleKey("  Не  пушить в MAIN. ") != RuleKey("не пушить в main") {
		t.Fatal("RuleKey должен сворачивать регистр, пробелы и точку в конце")
	}
	if RuleKey("а") == RuleKey("б") {
		t.Fatal("разные правила — разные ключи")
	}
}

func dec(d string, kind DecisionKind, text string) Decision {
	return Decision{Day: day(d), Kind: kind, Text: text}
}

func TestAssessHealth(t *testing.T) {
	today := "2026-11-10"
	fresh := func() HealthInput {
		return HealthInput{
			Today:     day(today),
			Decisions: []Decision{dec(today, DecisionReview, ""), dec(today, DecisionConfirmRule, "Правило один"), dec(today, DecisionConfirmRule, "Правило два")},
			Rules:     []string{"Правило один", "Правило два"},
			SealOK:    true,
		}
	}
	part := func(h Health, id string) HealthPart {
		for _, p := range h.Parts {
			if p.ID == id {
				return p
			}
		}
		t.Fatalf("нет части %q", id)
		return HealthPart{}
	}

	tests := []struct {
		name   string
		mut    func(in *HealthInput)
		total  int
		mode   TrustMode
		check  func(t *testing.T, h Health)
		reason []ReasonKind
	}{
		{name: "всё свежее", mut: func(*HealthInput) {}, total: 100, mode: TrustOK},
		{name: "20 дней без решений, ждать нечему: тишина не наказывается", mut: func(in *HealthInput) {
			in.Decisions = []Decision{dec("2026-10-21", DecisionReview, ""), dec("2026-10-21", DecisionConfirmRule, "Правило один"), dec("2026-10-21", DecisionConfirmRule, "Правило два")}
		}, total: 93, mode: TrustOK, check: func(t *testing.T, h Health) {
			if h.IdleDays != 20 || part(h, "rhythm").Value != 35 {
				t.Fatalf("idle=%d rhythm=%v", h.IdleDays, part(h, "rhythm").Value)
			}
		}},
		{name: "30 дней без решений, правила на границе свежести — молчим", mut: func(in *HealthInput) {
			in.Decisions = []Decision{dec("2026-10-11", DecisionApproveHot, "Правило один"), dec("2026-10-11", DecisionApproveHot, "Правило два")}
			in.Rules = []string{"Правило один", "Правило два"}
		}, total: 90, mode: TrustOK},
		{name: "ждёт устаревшее правило и 20 дней без решений — ритм снимает", mut: func(in *HealthInput) {
			in.Decisions = []Decision{dec("2026-10-21", DecisionReview, ""), dec("2026-10-01", DecisionConfirmRule, "Правило один")}
			in.Rules = []string{"Правило один"}
		}, total: 67, mode: TrustWarn, reason: []ReasonKind{ReasonRules, ReasonRhythm}, check: func(t *testing.T, h Health) {
			if part(h, "rhythm").Value != 15 {
				t.Fatalf("rhythm = %v", part(h, "rhythm").Value)
			}
		}},
		{name: "обслуживание не сбрасывает давность осмотра", mut: func(in *HealthInput) {
			in.Decisions = []Decision{dec("2026-10-01", DecisionReview, ""), dec(today, DecisionTopics, ""), dec(today, DecisionIndex, ""), dec(today, DecisionPortrait, "")}
			in.Rules = nil
		}, total: 100, mode: TrustOK, check: func(t *testing.T, h Health) {
			if h.IdleDays != 40 {
				t.Fatalf("idle = %d, want 40", h.IdleDays)
			}
		}},
		{name: "очередь: два предложения, старшее 17 дней — причина названа, баллов не снято", mut: func(in *HealthInput) {
			in.Queue = []PendingItem{{Name: "a", Since: day("2026-10-24")}, {Name: "b", Since: day("2026-11-08")}}
		}, total: 100, mode: TrustOK, reason: []ReasonKind{ReasonQueue}, check: func(t *testing.T, h Health) {
			if p := part(h, "queue"); p.Value != 25 || h.OldestPendingDays != 17 {
				t.Fatalf("queue=%v oldest=%d", p.Value, h.OldestPendingDays)
			}
		}},
		{name: "очередь за мягким потолком: 12 предложений — по два балла за лишнее", mut: func(in *HealthInput) {
			for range 12 {
				in.Queue = append(in.Queue, PendingItem{Name: "n", Since: day("2026-11-09")})
			}
		}, total: 96, mode: TrustOK, reason: []ReasonKind{ReasonQueue}},
		{name: "печать нарушена — не доверяю при любом счете", mut: func(in *HealthInput) { in.SealOK = false },
			total: 88, mode: TrustBad, reason: []ReasonKind{ReasonSeal}},
		{name: "векторы отстали", mut: func(in *HealthInput) { in.StaleVectors = 3 },
			total: 94, mode: TrustOK, reason: []ReasonKind{ReasonVectors}},
		{name: "правило без отметки считается устаревшим", mut: func(in *HealthInput) {
			in.Rules = []string{"Правило один", "Правило без следа"}
		}, total: 90, mode: TrustOK, reason: []ReasonKind{ReasonRules}, check: func(t *testing.T, h Health) {
			if h.StaleRules != 1 || h.Rules[1].AgeDays != -1 || !h.Rules[1].Stale {
				t.Fatalf("rules = %+v stale=%d", h.Rules, h.StaleRules)
			}
		}},
		{name: "правило на 30 днях — половина свежести", mut: func(in *HealthInput) {
			in.Decisions = []Decision{dec(today, DecisionReview, ""), dec("2026-10-11", DecisionApproveHot, "Правило один"), dec(today, DecisionConfirmRule, "Правило два")}
		}, total: 95, mode: TrustOK, check: func(t *testing.T, h Health) {
			if h.StaleRules != 0 || h.Rules[0].AgeDays != 30 {
				t.Fatalf("rules = %+v", h.Rules)
			}
		}},
		{name: "правило находится по ключу, а не по точному тексту", mut: func(in *HealthInput) {
			in.Rules = []string{"  правило ОДИН. ", "Правило два"}
		}, total: 100, mode: TrustOK},
		{name: "решений нет, записи лежат, ждать нечему", mut: func(in *HealthInput) {
			in.Decisions = nil
			in.Notes = 2
			in.Rules = nil
		}, total: 100, mode: TrustOK, check: func(t *testing.T, h Health) {
			if h.IdleDays != -1 {
				t.Fatalf("idle = %d, want -1", h.IdleDays)
			}
		}},
		{name: "пустая новая память не ругается", mut: func(in *HealthInput) {
			in.Decisions, in.Rules = nil, nil
		}, total: 100, mode: TrustOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := fresh()
			tt.mut(&in)
			h := AssessHealth(in)
			if h.Total != tt.total || h.Mode != tt.mode {
				t.Fatalf("total=%d mode=%s, want %d %s; parts=%+v", h.Total, h.Mode, tt.total, tt.mode, h.Parts)
			}
			var kinds []ReasonKind
			for _, r := range h.Reasons {
				kinds = append(kinds, r.Kind)
			}
			if len(kinds) != len(tt.reason) {
				t.Fatalf("reasons = %v, want %v", kinds, tt.reason)
			}
			for i := range kinds {
				if kinds[i] != tt.reason[i] {
					t.Fatalf("reasons = %v, want %v", kinds, tt.reason)
				}
			}
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

func TestTrustMode_Effect(t *testing.T) {
	if TrustOK.AgentMayPropose() != true || TrustWarn.AgentMayPropose() != true || TrustBad.AgentMayPropose() != false {
		t.Fatal("в режиме «не доверяю» предложения агента приостановлены, в остальных идут")
	}
}

// Только невнимание, без единой другой беды, доводит до «не доверяю»: полгода без осмотра и
// все правила устарели (ревью: раньше выходило ровно 45 и режим застревал на «с оговоркой»).
func TestAssessHealth_neglectAloneReachesBad(t *testing.T) {
	in := HealthInput{
		Today:     day("2026-11-10"),
		Decisions: []Decision{dec("2026-05-01", DecisionApproveHot, "Правило один")},
		Rules:     []string{"Правило один"},
		SealOK:    true,
	}
	h := AssessHealth(in)
	if h.Mode != TrustBad {
		t.Fatalf("mode = %s, total = %d", h.Mode, h.Total)
	}
}

// Дата из будущего (сбой часов, подделка) не замораживает свежесть.
func TestAssessHealth_ignoresDecisionsFromTheFuture(t *testing.T) {
	in := HealthInput{
		Today:     day("2026-11-10"),
		Decisions: []Decision{dec("2099-01-01", DecisionReview, ""), dec("2099-01-01", DecisionConfirmRule, "Правило один")},
		Rules:     []string{"Правило один"},
		SealOK:    true,
	}
	h := AssessHealth(in)
	if h.IdleDays != -1 || h.Rules[0].AgeDays != -1 || !h.Rules[0].Stale {
		t.Fatalf("idle=%d rules=%+v", h.IdleDays, h.Rules)
	}
}

// Повторное одобрение уже лежащего правила не освежает его: никто его не перечитывал.
func TestParseDecision_duplicateApprovalDoesNotDateTheRule(t *testing.T) {
	d, ok := ParseDecision("2026-11-01 10:05 одобрено в горячий слой: x — «правило один» (такое правило уже было)")
	if !ok || d.Kind != DecisionApproveDup || !d.Kind.IsJudgment() || d.Text != "правило один" {
		t.Fatalf("got %+v ok=%v", d, ok)
	}
	h := AssessHealth(HealthInput{Today: day("2026-11-10"), Decisions: []Decision{d}, Rules: []string{"Правило один"}, SealOK: true})
	if h.Rules[0].AgeDays != -1 {
		t.Fatalf("правило не должно свежеть от дубля: %+v", h.Rules[0])
	}
}
