package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Агент через MCP может найти и предложить, и только это: предложение уходит в
// очередь pending/, холодный и горячий слои не меняются.
func TestMemoryAgent_proposeGoesToTheQueueOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dvk-memory")
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	svc := memory.New(st, memory.Limits{})
	if _, err := svc.Add("pricing", "цены", "цену называть от нижней границы", "тело"); err != nil {
		t.Fatal(err)
	}
	agent := memoryAgent{dir: dir}

	if err := agent.Propose("new-rule", "git", "не пушить в main", "почему"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.Pending(); len(left) != 1 || left[0].Note.Origin != domain.OriginAgent {
		t.Fatalf("pending = %+v", left)
	}
	snap, _ := st.Load()
	if len(snap.Notes) != 1 {
		t.Errorf("холодный слой тронут: %+v", snap.Notes)
	}
	if err := agent.Propose("Bad Name", "git", "x", ""); !errors.Is(err, domain.ErrInvalidNote) {
		t.Errorf("негодное имя: %v", err)
	}

	hits, via, err := agent.Find("цену", 5)
	if err != nil || via != "text" || len(hits) != 1 || hits[0].Name != "pricing" {
		t.Fatalf("hits=%+v via=%q err=%v", hits, via, err)
	}
}

func TestMemoryAgent_noMemoryYetIsAnError(t *testing.T) {
	agent := memoryAgent{dir: filepath.Join(t.TempDir(), "нет")}
	if err := agent.Propose("a", "t", "d", ""); err == nil {
		t.Error("памяти еще нет: предложение некуда класть")
	}
}

// Режим доверия агента идет из того же здоровья, что видит окно: свежая память —
// «ok», правка HOT.md мимо системы — «bad», устаревшие правила называются.
func TestMemoryAgent_trustFollowsTheHealth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dvk-memory")
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	agent := memoryAgent{dir: dir}
	if tr, err := agent.Trust(); err != nil || tr.Mode != "ok" {
		t.Fatalf("свежая память: %+v %v", tr, err)
	}
	hot := "# HOT\n\n" + domain.RulesHeading + "\n\n- Правило без отметки\n"
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(hot), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := agent.Trust()
	if err != nil || tr.Mode != "bad" || len(tr.StaleRules) != 1 || tr.StaleRules[0] != "Правило без отметки" {
		t.Fatalf("правка мимо системы: %+v %v", tr, err)
	}
	if _, err := (memoryAgent{dir: filepath.Join(t.TempDir(), "нет")}).Trust(); err == nil {
		t.Error("памяти нет: режим не определить")
	}
}

// Завал в очереди доходит до агента: при QueueHardCap предложениях Trust называет очередь полной, на одно меньше — нет.
func TestMemoryAgent_trustReportsAFullQueue(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dvk-memory")
	if err := memorydir.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	agent := memoryAgent{dir: dir}
	for i := range domain.QueueHardCap {
		if i == domain.QueueHardCap-1 {
			if tr, err := agent.Trust(); err != nil || tr.QueueFull {
				t.Fatalf("%d предложений: очередь ещё не полна: %+v %v", i, tr, err)
			}
		}
		if err := agent.Propose(fmt.Sprintf("rule-%d", i), "git", "правило", ""); err != nil {
			t.Fatal(err)
		}
	}
	if tr, err := agent.Trust(); err != nil || !tr.QueueFull || tr.Mode != "ok" {
		t.Fatalf("%d предложений: очередь полна, режим не меняется: %+v %v", domain.QueueHardCap, tr, err)
	}
}
