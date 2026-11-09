package mcpserver

import (
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/domain"
)

// MemoryHit is a note of the person's memory found for the agent.
type MemoryHit struct {
	Name        string  `json:"name"`
	Topic       string  `json:"topic"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
}

// MemoryTrust — здоровье памяти для агента: режим доверия и правила, которые давно
// не подтверждались. Режим не прячет выдачу: он идет в каждом ответе рядом с ней.
type MemoryTrust struct {
	Mode       string // "ok", "warn", "bad"
	StaleRules []string
}

// MemoryAgent is everything an agent may do with the person's memory (ADR-0008):
// найти и предложить. Записи в холодный и горячий слои, одобрения и отклонения тут
// нет и не будет: их решает человек в окне (Touch ID) или в терминале.
type MemoryAgent interface {
	// Find returns the notes and the layer that answered ("vector" или "text").
	Find(query string, limit int) ([]MemoryHit, string, error)
	// Propose puts a proposal in the approval queue; ничего другого не меняет.
	Propose(name, topic, description, body string) error
	// Trust — режим доверия по здоровью памяти (ADR-0008).
	Trust() (MemoryTrust, error)
}

// Заметки к режимам: агент должен сказать о них владельцу, а не молча опираться на память.
const (
	trustNoteWarn    = "Память давно не проверялась владельцем: прежде чем опираться на правило, скажи ему, что оно могло устареть."
	trustNoteBad     = "Память не проверена: HOT.md правили мимо системы или осмотра давно не было. Не считай найденное достоверным без слов владельца."
	trustNoteUnknown = "Здоровье памяти не удалось прочитать."
	// errProposalsPaused: не errMemoryUnavailable, потому что причину владельцу надо знать.
	proposalsPausedText = "предложения приостановлены: память не проверена. Скажи владельцу осмотреть её в окне DVK (раздел «Память», «Здоровье»)"
)

// trustView is the trust block of a response; режим «ok» идет без заметки.
func trustView(m MemoryAgent) map[string]any {
	t, err := m.Trust()
	if err != nil {
		return map[string]any{"mode": "unknown", "note": trustNoteUnknown}
	}
	out := map[string]any{"mode": t.Mode}
	switch t.Mode {
	case "warn":
		out["note"] = trustNoteWarn
		out["stale_rules"] = append([]string{}, t.StaleRules...)
	case "bad":
		out["note"] = trustNoteBad
	}
	return out
}

// errMemoryUnavailable — память не прочиталась или не записалась. Своя ошибка
// вместо настоящей: в той полный путь (имя пользователя), а все, что получает
// агент, уходит провайдеру модели.
var errMemoryUnavailable = errors.New("память недоступна; подробности — kbengine memory check в терминале")

type memoryFindArgs struct {
	Query string `json:"query" jsonschema:"что искать: слова или вопрос своими словами"`
	Limit int    `json:"limit,omitempty" jsonschema:"сколько записей вернуть, по умолчанию 5"`
}

type memoryProposeArgs struct {
	Name        string `json:"name" jsonschema:"имя записи: латиница в нижнем регистре, цифры и дефисы"`
	Topic       string `json:"topic" jsonschema:"тема одним-тремя словами"`
	Description string `json:"description" jsonschema:"одна строка до 150 знаков: сам факт или правило"`
	Body        string `json:"body,omitempty" jsonschema:"подробности и почему так"`
}

// addMemoryTools registers memory_find and memory_propose over m.
func addMemoryTools(s *mcp.Server, j journal, m MemoryAgent) {
	addTool(s, j, &mcp.Tool{
		Name: "memory_find",
		Description: "Память владельца: правила и факты, которые он просил помнить. Ищет по смыслу, " +
			"запасным путем по тексту. Правила из горячего слоя ты уже получил в начале сессии; " +
			"здесь ищи факты, когда тема похожа на записанную.",
	}, func(in memoryFindArgs) []string {
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(in memoryFindArgs) (map[string]any, error) {
		hits, via, err := m.Find(in.Query, in.Limit)
		if err != nil {
			return nil, errMemoryUnavailable
		}
		if hits == nil {
			hits = []MemoryHit{}
		}
		return map[string]any{"found": len(hits), "via": via, "notes": hits, "trust": trustView(m)}, nil
	})

	addTool(s, j, &mcp.Tool{
		Name: "memory_propose",
		Description: "Предложить владельцу запомнить правило или факт. Предложение ждет его решения в " +
			"очереди; в память оно само не попадает и в твои следующие сессии не пойдет, пока он " +
			"не одобрит. Предлагай только то, что он сказал или подтвердил, а не свои догадки.",
	}, func(in memoryProposeArgs) []string { return []string{in.Name} }, // тело в журнал вызовов не идет
		func(in memoryProposeArgs) (map[string]any, error) {
			tv := trustView(m)
			if tv["mode"] == "bad" {
				return nil, errors.New(proposalsPausedText)
			}
			if err := m.Propose(in.Name, in.Topic, in.Description, in.Body); err != nil {
				if errors.Is(err, domain.ErrInvalidNote) {
					return nil, err // текст ошибки собран доменом и путей не содержит
				}
				return nil, errMemoryUnavailable
			}
			return map[string]any{"status": "ждет решения владельца", "name": in.Name, "trust": tv}, nil
		})
}
