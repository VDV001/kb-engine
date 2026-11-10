package memory

import (
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
)

// allDecisions — сколько строк журнала читает здоровье: все, что есть. Журнал
// растет на строку за решение человека, до предела тут далеко.
const allDecisions = 1 << 20

// Health measures how well the memory is kept (ADR-0008): ритм решений, очередь,
// целостность и свежесть правил. Читает только то, что система и так пишет.
func (s *Service) Health() (domain.Health, error) {
	snap, err := s.store.Load()
	if err != nil {
		return domain.Health{}, err
	}
	pending, err := s.store.Pending()
	if err != nil {
		return domain.Health{}, err
	}
	lines, err := s.store.Decisions(allDecisions)
	if err != nil {
		return domain.Health{}, err
	}
	h, _, err := s.assess(snap, pending, lines)
	return h, err
}

// assess computes the health from already loaded parts and returns the parsed
// decisions with it: окну нужны и счет, и события для ритма.
func (s *Service) assess(snap Snapshot, pending []PendingNote, lines []string) (domain.Health, []domain.Decision, error) {
	st, err := s.state(snap)
	if err != nil {
		return domain.Health{}, nil, err
	}
	var events []domain.Decision
	for _, l := range lines {
		if d, ok := domain.ParseDecision(l); ok {
			events = append(events, d)
		}
	}
	today := domain.DayOf(s.now())
	queue := make([]domain.PendingItem, 0, len(pending))
	for _, p := range pending {
		since := today // дата файла неизвестна: ждет «с сегодня», штраф за возраст не выдумываем
		if !p.Since.IsZero() {
			since = domain.DayOf(p.Since)
		}
		queue = append(queue, domain.PendingItem{Name: p.Note.Name, Since: since})
	}
	return domain.AssessHealth(domain.HealthInput{
		Today:        today,
		Decisions:    events,
		Rules:        domain.RulesOf(snap.Hot),
		Queue:        queue,
		SealOK:       st.SealDigest == "" || st.SealDigest == st.HotDigest,
		StaleVectors: domain.StaleVectors(st),
	}), events, nil
}

// Review records that the person looked through the memory and changed nothing:
// давность осмотра считается по таким отметкам и по любым другим суждениям.
func (s *Service) Review() error {
	return s.decide("%sбез изменений", domain.DecisionPrefixReview)
}

// ConfirmRule records that the person re-read a rule of the hot layer and keeps it.
func (s *Service) ConfirmRule(text string) error { return s.ConfirmRules([]string{text}) }

// ConfirmRules confirms several rules at once: все или ничего. Каждое правило ищется
// в HOT.md по ключу (регистр, пробелы, точка), в журнал идет его текст из файла: по
// нему здоровье потом узнает правило. Выдуманное правило отвергается до первой
// записи: подтверждение не должно придумывать то, чего нет.
func (s *Service) ConfirmRules(texts []string) error {
	snap, err := s.store.Load()
	if err != nil {
		return err
	}
	byKey := make(map[string]string)
	for _, r := range domain.RulesOf(snap.Hot) {
		byKey[domain.RuleKey(r)] = r
	}
	found := make([]string, 0, len(texts))
	for _, t := range texts {
		r, ok := byKey[domain.RuleKey(t)]
		if !ok {
			return fmt.Errorf("%w: %q", domain.ErrNoSuchRule, t)
		}
		found = append(found, r)
	}
	// ponytail: проверка до записи, а не транзакция: сбой диска посреди цикла оставит
	// часть отметок. Путь вверх: одна запись журнала со списком правил.
	for _, r := range found {
		if err := s.decide("%s%s", domain.DecisionPrefixConfirmRule, r); err != nil {
			return err
		}
	}
	return nil
}
