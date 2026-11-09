package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

type stubMemory struct {
	hits     []mcpserver.MemoryHit
	proposed [][4]string
	fail     bool
}

const memFailText = "память: /Users/someone/Library/Application Support/kb-desktop/dvk-memory/cold/x.md: permission denied"

func (s *stubMemory) Find(q string, limit int) ([]mcpserver.MemoryHit, string, error) {
	if s.fail {
		return nil, "", errors.New(memFailText)
	}
	return s.hits, "text", nil
}

func (s *stubMemory) Propose(name, topic, description, body string) error {
	if s.fail {
		return errors.New(memFailText)
	}
	s.proposed = append(s.proposed, [4]string{name, topic, description, body})
	return nil
}

func connectMemory(t *testing.T, m mcpserver.MemoryAgent) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Memory: m})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Агенту отдаются два инструмента: найти и предложить. Ни approve, ни записи в
// холодный или горячий слой у него нет: человек решает в окне (ревью #521).
func TestServer_memoryTools(t *testing.T) {
	if names := toolNames(t, connect(t, stubQuerier{entries: fixture(t)})); names["memory_find"] || names["memory_propose"] {
		t.Fatal("инструменты памяти объявлены без памяти")
	}
	m := &stubMemory{hits: []mcpserver.MemoryHit{{Name: "pricing", Topic: "цены", Description: "цену называть от нижней границы", Score: 0.71}}}
	cs := connectMemory(t, m)
	names := toolNames(t, cs)
	if !names["memory_find"] || !names["memory_propose"] {
		t.Fatalf("инструменты: %v", names)
	}
	for n := range names {
		if strings.Contains(n, "approve") || strings.Contains(n, "reject") {
			t.Errorf("агенту отдан инструмент решения %q", n)
		}
	}
	raw, _ := json.Marshal(callTool(t, cs, "memory_find", map[string]any{"query": "цена"}))
	if !strings.Contains(string(raw), "pricing") || !strings.Contains(string(raw), "text") {
		t.Errorf("find: %s", raw)
	}
	res := callTool(t, cs, "memory_propose", map[string]any{"name": "new-rule", "topic": "git", "description": "не пушить в main", "body": "почему"})
	if res.IsError || len(m.proposed) != 1 || m.proposed[0] != [4]string{"new-rule", "git", "не пушить в main", "почему"} {
		t.Fatalf("propose: %+v %v", res, m.proposed)
	}
	raw, _ = json.Marshal(res)
	if !strings.Contains(string(raw), "ждет") {
		t.Errorf("ответ должен сказать, что предложение ждет человека: %s", raw)
	}
}

func TestServer_memoryTools_failureLeaksNoPaths(t *testing.T) {
	cs := connectMemory(t, &stubMemory{fail: true})
	for tool, args := range map[string]map[string]any{
		"memory_find":    {"query": "x"},
		"memory_propose": {"name": "a", "topic": "t", "description": "d"},
	} {
		res := callTool(t, cs, tool, args)
		raw, _ := json.Marshal(res)
		if !res.IsError || strings.Contains(string(raw), "/Users/someone") {
			t.Errorf("%s: isError=%v %s", tool, res.IsError, raw)
		}
	}
}
