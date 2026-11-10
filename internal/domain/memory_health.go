package domain

import (
	"math"
	"slices"
	"strings"
	"time"
)

// Здоровье памяти (ADR-0008): чем реже человек её смотрит, тем меньше оснований
// ей доверять. Здоровье считается только по тому, что система и так пишет: журнал
// решений, очередь одобрения, печать HOT.md, отпечатки векторов. Формула не
// откалибрована на данных: пороги и веса выбраны, а не измерены (ponytail: потолок
// — выдуманные веса; путь вверх — подобрать их по тому, где память подвела агента).

// DecisionKind — вид решения человека в журнале.
type DecisionKind string

// Виды решений. Первые пять — суждение человека и сбрасывают давность осмотра;
// остальные — обслуживание, оно журналируется, но осмотром не считается.
const (
	DecisionApproveHot DecisionKind = "approve-hot"
	// DecisionApproveDup — одобрено правило, которое уже лежало в HOT.md: суждение
	// человека, но правило оно не освежает (его никто не перечитывал).
	DecisionApproveDup  DecisionKind = "approve-hot-dup"
	DecisionApproveCold DecisionKind = "approve-cold"
	DecisionReject      DecisionKind = "reject"
	DecisionSeal        DecisionKind = "seal"
	DecisionReview      DecisionKind = "review"
	DecisionConfirmRule DecisionKind = "confirm-rule"
	DecisionTopics      DecisionKind = "topics"
	DecisionPortrait    DecisionKind = "portrait"
	DecisionIndex       DecisionKind = "index"
	DecisionOther       DecisionKind = "other"
)

// IsJudgment says whether the decision is a human judgment: решение — суждение человека, а не обслуживание перечня и индекса.
func (k DecisionKind) IsJudgment() bool {
	switch k {
	case DecisionApproveHot, DecisionApproveDup, DecisionApproveCold, DecisionReject, DecisionSeal, DecisionReview, DecisionConfirmRule:
		return true
	}
	return false
}

// Decision — одна строка журнала решений, разобранная.
type Decision struct {
	// Day — календарный день решения (полночь, UTC-метка без часового пояса).
	Day  time.Time
	Kind DecisionKind
	// Text — что решали: текст правила или имя записи; у остальных пусто.
	Text string
}

// Тексты строк журнала: их пишет сценарий памяти, а здоровье читает по ним.
// Одна таблица на оба конца, чтобы формулировка не разошлась с разбором.
const (
	DecisionPrefixApproveHot  = "одобрено в горячий слой: "
	DecisionSuffixDup         = " (такое правило уже было)"
	DecisionPrefixApproveCold = "одобрено в холодный слой: "
	DecisionPrefixReject      = "отклонено: "
	DecisionTextSeal          = "подтверждено: текущий HOT.md принят как есть"
	DecisionPrefixReview      = "осмотр: "
	DecisionPrefixConfirmRule = "правило подтверждено: "
	DecisionTextTopics        = "перечень тем в HOT.md пересобран из тем записей"
	DecisionTextPortrait      = "блок «Кто я» в HOT.md обновлен из одобренных фактов портрета"
	DecisionTextIndex         = "индекс векторов пересобран"
)

// DayOf returns the calendar day of t as a midnight UTC mark: дни сравниваются
// без часового пояса и без перехода на летнее время.
func DayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

const decisionStamp = "2006-01-02 15:04"

// ParseDecision reads a journal line ("2026-11-01 10:05 текст"); строка без
// даты — не решение (ok=false). Неизвестный текст сохраняет дату как DecisionOther.
func ParseDecision(line string) (Decision, bool) {
	if len(line) < len(decisionStamp) {
		return Decision{}, false
	}
	at, err := time.Parse(decisionStamp, line[:len(decisionStamp)])
	if err != nil {
		return Decision{}, false
	}
	day := DayOf(at)
	rest := strings.TrimSpace(line[len(decisionStamp):])
	d := Decision{Day: day, Kind: DecisionOther, Text: rest}
	for _, r := range decisionRules {
		if (r.exact && rest == r.pat) || (!r.exact && strings.HasPrefix(rest, r.pat)) {
			d.Kind, d.Text = r.kind, r.text(rest)
			if d.Kind == DecisionApproveHot && strings.HasSuffix(rest, DecisionSuffixDup) {
				d.Kind = DecisionApproveDup
			}
			break
		}
	}
	return d, true
}

// decisionRule maps a journal text to its kind and to the text worth keeping.
type decisionRule struct {
	pat   string
	exact bool // весь текст равен pat, иначе pat — префикс
	kind  DecisionKind
	text  func(rest string) string
}

var decisionRules = []decisionRule{
	{DecisionPrefixApproveHot, false, DecisionApproveHot, quoted},
	{DecisionPrefixApproveCold, false, DecisionApproveCold, func(r string) string { return nameAfter(r, DecisionPrefixApproveCold) }},
	{DecisionPrefixReject, false, DecisionReject, func(r string) string { return nameAfter(r, DecisionPrefixReject) }},
	{DecisionTextSeal, true, DecisionSeal, func(string) string { return "" }},
	{DecisionPrefixReview, false, DecisionReview, func(string) string { return "" }},
	{DecisionPrefixConfirmRule, false, DecisionConfirmRule, func(r string) string { return strings.TrimSpace(strings.TrimPrefix(r, DecisionPrefixConfirmRule)) }},
	{DecisionTextTopics, true, DecisionTopics, func(string) string { return "" }},
	{DecisionTextPortrait, true, DecisionPortrait, func(string) string { return "" }},
	{DecisionTextIndex, true, DecisionIndex, func(string) string { return "" }},
}

// quoted returns the text inside the first «…» up to the last », the rule as it
// went to HOT.md; хвост "(такое правило уже было)" остаётся снаружи.
func quoted(s string) string {
	_, rest, ok := strings.Cut(s, "«")
	if !ok {
		return ""
	}
	if i := strings.LastIndex(rest, "»"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// nameAfter returns the note name that follows the prefix, up to the first space.
func nameAfter(s, prefix string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(s, prefix), " ")
	return name
}

// RulesOf lists the rules of the hot layer: пункты раздела RulesHeading, кроме
// пустого маркера из скелета.
func RulesOf(hot string) []string {
	lines := strings.Split(hot, "\n")
	h := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == RulesHeading {
			h = i
			break
		}
	}
	if h < 0 {
		return nil
	}
	var out []string
	for i := h + 1; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "- "); ok && strings.TrimSpace(rest) != "" {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

// RuleKey folds a rule to the key its decisions are matched by: регистр, пробелы и
// точка в конце не отличают одно правило от другого.
func RuleKey(s string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(s), " ")), ".")
}

// TrustMode — что память разрешает агенту при таком здоровье.
type TrustMode string

// Режимы доверия.
const (
	TrustOK   TrustMode = "ok"
	TrustWarn TrustMode = "warn"
	TrustBad  TrustMode = "bad"
)

// AgentMayPropose says whether the agent may queue proposals: в режиме «не доверяю» очередь предложений приостановлена —
// человек не успевает разбирать то, что уже копится.
func (m TrustMode) AgentMayPropose() bool { return m != TrustBad }

// Пороги и веса (см. оговорку о калибровке в начале файла).
const (
	// RuleStaleDays: правило без подтверждения дольше этого срока считается устаревшим.
	RuleStaleDays = 30
	// RuleLifeDays: за этот срок свежесть правила доходит до нуля.
	RuleLifeDays = 60
	// IdleWarnDays: после стольких дней без решений осмотр попадает в причины.
	IdleWarnDays = 7
	// QueueSoftCap: сколько предложений могут ждать без штрафа. Непринятое предложение
	// агенту не доверено, памяти оно не вредит: счёт падает только от завала сверх этого.
	QueueSoftCap = 10
	// QueueHardCap: столько предложений в очереди закрывают предложения агента независимо
	// от режима. Штраф очереди не больше 25 баллов, так что завал сам режим не опускает:
	// пауза идет по этому потолку, счёт и режим он не трогает.
	QueueHardCap = 30

	trustOKFrom   = 75
	trustWarnFrom = 50 // ниже 50 — «не доверяю»: одно невнимание (осмотра нет, правила остыли) даёт 45

	maxRhythm, maxQueue, maxIntegrity, maxRules = 35, 25, 20, 20
)

// PendingItem — предложение в очереди одобрения и с какого дня оно ждёт.
type PendingItem struct {
	Name  string
	Since time.Time
}

// HealthInput — всё, из чего считается здоровье.
type HealthInput struct {
	// Today — календарный день расчёта (как у Decision.Day).
	Today     time.Time
	Decisions []Decision
	Rules     []string
	// Notes — записей холодного слоя; нужен, чтобы пустая память не считалась запущенной.
	Notes        int
	Queue        []PendingItem
	SealOK       bool
	StaleVectors int
}

// HealthPart — одна составляющая счёта.
type HealthPart struct {
	ID         string
	Value, Max float64
}

// ReasonKind — почему здоровье не полное.
type ReasonKind string

// Причины, по порядку важности.
const (
	ReasonSeal    ReasonKind = "seal"
	ReasonQueue   ReasonKind = "queue"
	ReasonRules   ReasonKind = "rules"
	ReasonVectors ReasonKind = "vectors"
	ReasonRhythm  ReasonKind = "rhythm"
)

// HealthReason — причина и число, с которым её называют (предложений, правил, дней).
type HealthReason struct {
	Kind ReasonKind
	N    int
}

// RuleHealth — здоровье одного правила горячего слоя.
type RuleHealth struct {
	Text string
	// AgeDays — дней с последнего подтверждения человеком; -1 — отметки нет совсем.
	AgeDays int
	Stale   bool
}

// Health — итог: счёт, режим доверия, причины и правила с возрастом.
type Health struct {
	Parts   []HealthPart
	Total   int
	Mode    TrustMode
	Reasons []HealthReason
	// IdleDays — дней с последнего суждения человека; -1 — суждений в журнале нет.
	IdleDays          int
	LastJudgment      time.Time
	OldestPendingDays int
	// Pending — предложений в очереди одобрения.
	Pending    int
	StaleRules int
	Rules      []RuleHealth
}

// AgentMayPropose says whether the agent may queue proposals: режим не «не доверяю» и
// очередь не завалена (QueueHardCap).
func (h Health) AgentMayPropose() bool {
	return h.Mode.AgentMayPropose() && h.Pending < QueueHardCap
}

func daysBetween(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// AssessHealth computes the health of the memory. Режим «не доверяю» наступает при
// счёте ниже trustWarnFrom либо при правке HOT.md мимо системы: счёт этого не
// перекрывает.
func AssessHealth(in HealthInput) Health {
	h := Health{IdleDays: -1}
	// Дата позже сегодняшней (сбой часов, подделка строки) не освежает ничего.
	in.Decisions = slices.DeleteFunc(slices.Clone(in.Decisions), func(d Decision) bool { return d.Day.After(in.Today) })
	for _, d := range in.Decisions {
		if d.Kind.IsJudgment() && d.Day.After(h.LastJudgment) {
			h.LastJudgment = d.Day
		}
	}
	if !h.LastJudgment.IsZero() {
		h.IdleDays = max(0, daysBetween(h.LastJudgment, in.Today))
	}
	h.Rules = ruleHealths(in)
	h.Pending = len(in.Queue)
	for _, q := range in.Queue {
		h.OldestPendingDays = max(h.OldestPendingDays, daysBetween(q.Since, in.Today))
	}
	rules := rulesScore(&h)
	h.Parts = []HealthPart{
		{"rhythm", rhythmScore(oldestWaitingDays(in, h), awaitsReview(in, h)), maxRhythm},
		{"queue", queueScore(in), maxQueue},
		{"integrity", integrityScore(in), maxIntegrity},
		{"rules", rules, maxRules},
	}
	var sum float64
	for _, p := range h.Parts {
		sum += p.Value
	}
	h.Total = int(math.Round(sum))
	switch {
	case !in.SealOK || h.Total < trustWarnFrom:
		h.Mode = TrustBad
	case h.Total < trustOKFrom:
		h.Mode = TrustWarn
	default:
		h.Mode = TrustOK
	}
	h.Reasons = reasonsOf(in, h)
	return h
}

// awaitsReview: осмотра ждёт предложение в очереди или правило без свежего подтверждения.
func awaitsReview(in HealthInput, h Health) bool { return len(in.Queue) > 0 || h.StaleRules > 0 }

// oldestWaitingDays: сколько дней ждёт самое старое из того, что просит осмотра:
// предложение в очереди или правило сверх RuleStaleDays. Правило без отметки
// датируется днями без суждений; суждений нет совсем — возраст неизвестен, штрафа нет.
// Штраф растёт по дню, без скачка на границе срока.
func oldestWaitingDays(in HealthInput, h Health) int {
	days := 0
	if len(in.Queue) > 0 {
		days = h.OldestPendingDays
	}
	for _, r := range h.Rules {
		if !r.Stale {
			continue
		}
		age := r.AgeDays
		if age < 0 {
			age = h.IdleDays
		}
		days = max(days, age-RuleStaleDays)
	}
	return days
}

// rhythmScore: ожидание осмотра снимает баллы по дню, только когда есть что осматривать.
// Тихая память, где агент ничего не предлагал и правила свежи, не виновата в тишине.
func rhythmScore(waitingDays int, awaits bool) float64 {
	if !awaits {
		return maxRhythm
	}
	return max(0, maxRhythm-float64(waitingDays))
}

func queueScore(in HealthInput) float64 {
	return max(0, maxQueue-2*float64(max(0, len(in.Queue)-QueueSoftCap)))
}

func integrityScore(in HealthInput) float64 {
	v := max(0, 8-2*float64(in.StaleVectors))
	if in.SealOK {
		v += 12
	}
	return v
}

// rulesScore averages the freshness of the rules and counts the stale ones into h.
func rulesScore(h *Health) float64 {
	if len(h.Rules) == 0 {
		return maxRules
	}
	var fresh float64
	for _, r := range h.Rules {
		if r.Stale {
			h.StaleRules++
		}
		if r.AgeDays >= 0 {
			fresh += max(0, 1-float64(r.AgeDays)/RuleLifeDays)
		}
	}
	return fresh / float64(len(h.Rules)) * maxRules
}

func reasonsOf(in HealthInput, h Health) []HealthReason {
	var rs []HealthReason
	if !in.SealOK {
		rs = append(rs, HealthReason{ReasonSeal, 1})
	}
	if len(in.Queue) > 0 {
		rs = append(rs, HealthReason{ReasonQueue, len(in.Queue)})
	}
	if h.StaleRules > 0 {
		rs = append(rs, HealthReason{ReasonRules, h.StaleRules})
	}
	if in.StaleVectors > 0 {
		rs = append(rs, HealthReason{ReasonVectors, in.StaleVectors})
	}
	if awaitsReview(in, h) && (h.IdleDays > IdleWarnDays || h.IdleDays < 0) {
		rs = append(rs, HealthReason{ReasonRhythm, h.IdleDays})
	}
	return rs
}

// ruleHealths dates each rule by the latest approval or confirmation naming it.
func ruleHealths(in HealthInput) []RuleHealth {
	last := make(map[string]time.Time)
	for _, d := range in.Decisions {
		if d.Kind != DecisionApproveHot && d.Kind != DecisionConfirmRule {
			continue
		}
		if k := RuleKey(d.Text); d.Day.After(last[k]) {
			last[k] = d.Day
		}
	}
	out := make([]RuleHealth, 0, len(in.Rules))
	for _, r := range in.Rules {
		rh := RuleHealth{Text: r, AgeDays: -1, Stale: true}
		if at, ok := last[RuleKey(r)]; ok {
			rh.AgeDays = max(0, daysBetween(at, in.Today))
			rh.Stale = rh.AgeDays > RuleStaleDays
		}
		out = append(out, rh)
	}
	return out
}
