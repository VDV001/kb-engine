package domain

import (
	"math"
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
	DecisionApproveHot  DecisionKind = "approve-hot"
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

// IsJudgment: решение — суждение человека, а не обслуживание перечня и индекса.
func (k DecisionKind) IsJudgment() bool {
	switch k {
	case DecisionApproveHot, DecisionApproveCold, DecisionReject, DecisionSeal, DecisionReview, DecisionConfirmRule:
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
	switch {
	case strings.HasPrefix(rest, DecisionPrefixApproveHot):
		d.Kind, d.Text = DecisionApproveHot, quoted(rest)
	case strings.HasPrefix(rest, DecisionPrefixApproveCold):
		d.Kind, d.Text = DecisionApproveCold, nameAfter(rest, DecisionPrefixApproveCold)
	case strings.HasPrefix(rest, DecisionPrefixReject):
		d.Kind, d.Text = DecisionReject, nameAfter(rest, DecisionPrefixReject)
	case rest == DecisionTextSeal:
		d.Kind, d.Text = DecisionSeal, ""
	case strings.HasPrefix(rest, DecisionPrefixReview):
		d.Kind, d.Text = DecisionReview, ""
	case strings.HasPrefix(rest, DecisionPrefixConfirmRule):
		d.Kind, d.Text = DecisionConfirmRule, strings.TrimSpace(strings.TrimPrefix(rest, DecisionPrefixConfirmRule))
	case rest == DecisionTextTopics:
		d.Kind, d.Text = DecisionTopics, ""
	case rest == DecisionTextPortrait:
		d.Kind, d.Text = DecisionPortrait, ""
	case rest == DecisionTextIndex:
		d.Kind, d.Text = DecisionIndex, ""
	}
	return d, true
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

// AgentMayPropose: в режиме «не доверяю» очередь предложений приостановлена —
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
	// QueueGraceDays: предложение ждёт без штрафа за возраст столько дней.
	QueueGraceDays = 7

	trustOKFrom   = 75
	trustWarnFrom = 45

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
	StaleRules        int
	Rules             []RuleHealth
}

func daysBetween(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// AssessHealth computes the health of the memory. Режим «не доверяю» наступает при
// счёте ниже trustWarnFrom либо при правке HOT.md мимо системы: счёт этого не
// перекрывает.
func AssessHealth(in HealthInput) Health {
	h := Health{IdleDays: -1}
	for _, d := range in.Decisions {
		if d.Kind.IsJudgment() && d.Day.After(h.LastJudgment) {
			h.LastJudgment = d.Day
		}
	}
	if !h.LastJudgment.IsZero() {
		h.IdleDays = max(0, daysBetween(h.LastJudgment, in.Today))
	}

	h.Rules = ruleHealths(in)
	var fresh float64
	for _, r := range h.Rules {
		if r.Stale {
			h.StaleRules++
		}
		if r.AgeDays >= 0 {
			fresh += max(0, 1-float64(r.AgeDays)/RuleLifeDays)
		}
	}
	rules := float64(maxRules)
	if len(h.Rules) > 0 {
		rules = fresh / float64(len(h.Rules)) * maxRules
	}

	for _, q := range in.Queue {
		h.OldestPendingDays = max(h.OldestPendingDays, daysBetween(q.Since, in.Today))
	}
	queue := max(0, maxQueue-3*float64(len(in.Queue))-0.6*float64(max(0, h.OldestPendingDays-QueueGraceDays)))

	integrity := max(0, 8-2*float64(in.StaleVectors))
	if in.SealOK {
		integrity += 12
	}

	// Пустая память осмотра не требует; с чем-то на хранении отсутствие суждений — пробел.
	rhythm := float64(maxRhythm)
	hasStock := len(in.Rules)+in.Notes+len(in.Queue) > 0
	switch {
	case h.IdleDays >= 0:
		rhythm = max(0, maxRhythm-float64(h.IdleDays))
	case hasStock:
		rhythm = 0
	}

	h.Parts = []HealthPart{
		{"rhythm", rhythm, maxRhythm}, {"queue", queue, maxQueue},
		{"integrity", integrity, maxIntegrity}, {"rules", rules, maxRules},
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

	if !in.SealOK {
		h.Reasons = append(h.Reasons, HealthReason{ReasonSeal, 1})
	}
	if len(in.Queue) > 0 {
		h.Reasons = append(h.Reasons, HealthReason{ReasonQueue, len(in.Queue)})
	}
	if h.StaleRules > 0 {
		h.Reasons = append(h.Reasons, HealthReason{ReasonRules, h.StaleRules})
	}
	if in.StaleVectors > 0 {
		h.Reasons = append(h.Reasons, HealthReason{ReasonVectors, in.StaleVectors})
	}
	if (h.IdleDays > IdleWarnDays) || (h.IdleDays < 0 && hasStock) {
		h.Reasons = append(h.Reasons, HealthReason{ReasonRhythm, h.IdleDays})
	}
	return h
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
