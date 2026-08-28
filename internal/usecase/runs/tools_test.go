package runs_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// Имя вызова инструмента в журнале строится и разбирается ОДНИМ местом.
// Вторая копия формы имени разошлась бы с первой молча, и тогда писатель
// клал бы в журнал то, чего читатель не узнаёт, — счётчик показывал бы ноль
// при исправной записи.
func TestToolCommand_roundTrip(t *testing.T) {
	cmd := runs.ToolCommand("search_catalog")
	if cmd == "search_catalog" {
		t.Fatalf("имя вызова совпало с именем инструмента (%q) — читатель не отличит его от команды движка", cmd)
	}
	name, ok := runs.ToolOf(cmd)
	if !ok || name != "search_catalog" {
		t.Fatalf("ToolOf(%q) = %q, %v; ждали search_catalog, true", cmd, name, ok)
	}
	// Отрицательный контроль: обычная команда движка вызовом не считается,
	// иначе счётчик вызовов пополнялся бы каждым `kbengine audit`.
	if name, ok := runs.ToolOf("audit"); ok {
		t.Errorf("ToolOf(\"audit\") = %q, true; команда движка не вызов инструмента", name)
	}
}

// Вызовы инструментов лежат в одном журнале с командами и считаются ОТДЕЛЬНО.
//
// Смешать их нельзя в обе стороны: инструмент, попавший в список команд, стал
// бы «давно не запускавшимся» (порогов для вызовов нет вовсе), а движок объявил
// бы его командой, которой больше не знает.
func TestBuild_mcpCallsAreCountedApart(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	recs := []domain.RunRecord{
		at(t, "audit", now.Add(-2*time.Hour), 0),
		at(t, runs.ToolCommand("search_catalog"), now.Add(-time.Hour), 0, "кубернетес"),
		at(t, runs.ToolCommand("search_catalog"), now.Add(-30*time.Minute), 0, "ddd"),
		at(t, runs.ToolCommand("get_entry"), now.Add(-20*time.Minute), 1, "#9999"),
	}

	r, err := runs.Build(journalStub{recs: recs, exists: true}, known, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := len(r.Tools); got != 2 {
		t.Fatalf("инструментов в отчёте %d, ждали 2: %+v", got, r.Tools)
	}
	byName := map[string]runs.CommandStat{}
	for _, s := range r.Tools {
		byName[s.Name] = s
	}
	if s := byName["search_catalog"]; s.Runs != 2 || s.Failures != 0 {
		t.Errorf("search_catalog: прогонов %d, отказов %d; ждали 2 и 0", s.Runs, s.Failures)
	}
	// Отказ инструмента считается отказом: «спросили и не получили ответа» —
	// то, ради чего счётчик и заводится.
	if s := byName["get_entry"]; s.Runs != 1 || s.Failures != 1 {
		t.Errorf("get_entry: прогонов %d, отказов %d; ждали 1 и 1", s.Runs, s.Failures)
	}

	for _, c := range r.Commands {
		if _, isTool := runs.ToolOf(c.Name); isTool {
			t.Errorf("вызов %q попал в список команд движка", c.Name)
		}
	}
	for _, u := range r.Unknown {
		if _, isTool := runs.ToolOf(u); isTool {
			t.Errorf("вызов %q объявлен командой, которой движок не знает", u)
		}
	}
	// Имена инструментов в известные команды не входят, поэтому «не
	// запускалось ни разу» о них молчит — там перечисляются команды движка.
	if len(r.Commands) != 1 || r.Commands[0].Name != "audit" {
		t.Errorf("команды движка = %+v, ждали одну audit", r.Commands)
	}
}
