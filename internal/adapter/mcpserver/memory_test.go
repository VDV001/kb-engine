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
	trust    mcpserver.MemoryTrust
	trustErr error
}

// Trust: по умолчанию память в порядке, чтобы прежние тесты не знали о здоровье.
func (s *stubMemory) Trust() (mcpserver.MemoryTrust, error) {
	if s.trustErr != nil {
		return mcpserver.MemoryTrust{}, s.trustErr
	}
	if s.trust.Mode == "" {
		return mcpserver.MemoryTrust{Mode: "ok"}, nil
	}
	return s.trust, nil
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

func payload(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("пустой ответ: %+v", res)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &m); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	return m
}

func trustOf(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	tr, _ := payload(t, res)["trust"].(map[string]any)
	return tr
}

// Режим доверия виден агенту в каждом ответе, а не меняет выдачу молча (ADR-0008).
func TestServer_memoryFindCarriesTheTrustMode(t *testing.T) {
	hit := []mcpserver.MemoryHit{{Name: "pricing", Topic: "цены", Description: "цену называть от нижней границы", Score: 0.7}}
	tests := []struct {
		name  string
		trust mcpserver.MemoryTrust
		err   error
		mode  string
		note  string // подстрока заметки; "" — заметки нет
	}{
		{"ok без заметки", mcpserver.MemoryTrust{Mode: "ok"}, nil, "ok", ""},
		{"warn называет устаревшие правила", mcpserver.MemoryTrust{Mode: "warn", StaleRules: []string{"Релиз в пятницу не катим"}}, nil, "warn", "давно не проверялась"},
		{"bad говорит не доверять", mcpserver.MemoryTrust{Mode: "bad"}, nil, "bad", "не проверена"},
		{"здоровье не прочиталось", mcpserver.MemoryTrust{}, errors.New("диск"), "unknown", "не удалось"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := connectMemory(t, &stubMemory{hits: hit, trust: tt.trust, trustErr: tt.err})
			res := callTool(t, cs, "memory_find", map[string]any{"query": "цена"})
			if res.IsError {
				t.Fatalf("поиск не должен падать из-за здоровья: %+v", res)
			}
			m := payload(t, res)
			if m["found"] != float64(1) {
				t.Errorf("находки пропали: %v", m)
			}
			tr := trustOf(t, res)
			if tr["mode"] != tt.mode {
				t.Fatalf("trust = %v, want mode %s", tr, tt.mode)
			}
			note, _ := tr["note"].(string)
			if (tt.note == "") != (note == "") || !strings.Contains(note, tt.note) {
				t.Errorf("note = %q, want to contain %q", note, tt.note)
			}
			if tt.mode == "warn" && !strings.Contains(strings.Join(anyStrings(tr["stale_rules"]), "|"), "Релиз в пятницу") {
				t.Errorf("stale_rules = %v", tr["stale_rules"])
			}
		})
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// «Не доверяю»: очередь предложений приостановлена; остальное идет как шло.
func TestServer_memoryProposeIsPausedWhenTrustIsBad(t *testing.T) {
	m := &stubMemory{trust: mcpserver.MemoryTrust{Mode: "bad"}}
	cs := connectMemory(t, m)
	res := callTool(t, cs, "memory_propose", map[string]any{"name": "a", "topic": "t", "description": "d"})
	raw, _ := json.Marshal(res)
	if !res.IsError || len(m.proposed) != 0 || !strings.Contains(string(raw), "приостановлен") {
		t.Fatalf("isError=%v proposed=%v %s", res.IsError, m.proposed, raw)
	}
	for _, mode := range []string{"ok", "warn"} {
		m := &stubMemory{trust: mcpserver.MemoryTrust{Mode: mode}}
		res := callTool(t, connectMemory(t, m), "memory_propose", map[string]any{"name": "a", "topic": "t", "description": "d"})
		if res.IsError || len(m.proposed) != 1 || trustOf(t, res)["mode"] != mode {
			t.Errorf("%s: предложение должно идти и нести режим: %+v %v", mode, res, m.proposed)
		}
	}
}

// Если здоровье не прочиталось, очередь не открывается: непроверенное не считается безопасным.
func TestServer_memoryProposeFailsClosedWhenTrustIsUnknown(t *testing.T) {
	m := &stubMemory{trustErr: errors.New("диск")}
	res := callTool(t, connectMemory(t, m), "memory_propose", map[string]any{"name": "a", "topic": "t", "description": "d"})
	if !res.IsError || len(m.proposed) != 0 {
		t.Fatalf("isError=%v proposed=%v", res.IsError, m.proposed)
	}
}

// Список правил без подтверждения идет агенту в любом режиме, где он есть.
func TestServer_memoryFindNamesStaleRulesInEveryMode(t *testing.T) {
	for _, mode := range []string{"ok", "warn", "bad"} {
		m := &stubMemory{trust: mcpserver.MemoryTrust{Mode: mode, StaleRules: []string{"Старое правило"}}}
		tr := trustOf(t, callTool(t, connectMemory(t, m), "memory_find", map[string]any{"query": "x"}))
		if strings.Join(anyStrings(tr["stale_rules"]), "|") != "Старое правило" {
			t.Errorf("%s: stale_rules = %v", mode, tr["stale_rules"])
		}
	}
}
