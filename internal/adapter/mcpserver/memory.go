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

// MemoryAgent is everything an agent may do with the person's memory (ADR-0008):
// найти и предложить. Записи в холодный и горячий слои, одобрения и отклонения тут
// нет и не будет: их решает человек в окне (Touch ID) или в терминале.
type MemoryAgent interface {
	// Find returns the notes and the layer that answered ("vector" или "text").
	Find(query string, limit int) ([]MemoryHit, string, error)
	// Propose puts a proposal in the approval queue; ничего другого не меняет.
	Propose(name, topic, description, body string) error
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
		return map[string]any{"found": len(hits), "via": via, "notes": hits}, nil
	})

	addTool(s, j, &mcp.Tool{
		Name: "memory_propose",
		Description: "Предложить владельцу запомнить правило или факт. Предложение ждет его решения в " +
			"очереди; в память оно само не попадает и в твои следующие сессии не пойдет, пока он " +
			"не одобрит. Предлагай только то, что он сказал или подтвердил, а не свои догадки.",
	}, func(in memoryProposeArgs) []string { return []string{in.Name} }, // тело в журнал вызовов не идет
		func(in memoryProposeArgs) (map[string]any, error) {
			if err := m.Propose(in.Name, in.Topic, in.Description, in.Body); err != nil {
				if errors.Is(err, domain.ErrInvalidNote) {
					return nil, err // текст ошибки собран доменом и путей не содержит
				}
				return nil, errMemoryUnavailable
			}
			return map[string]any{"status": "ждет решения владельца", "name": in.Name}, nil
		})
}
