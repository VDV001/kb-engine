package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// Вызов инструмента доезжает до ТОГО ЖЕ журнала, который читает `kbengine runs`.
//
// Проверяется на настоящем файле, а не на порте: между «адаптер позвал запись»
// и «запись оказалась в журнале, который читают» уже был разрыв — карточка
// финансов писала строку, которую движок не мог сопоставить со своей.
func TestJournalRecorder_writesWhereRunsReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	var stderr bytes.Buffer

	r := newJournalRecorder(path, func() time.Time { return now }, &stderr)
	r.RecordCall("search_catalog", []string{"кубернетес"}, now.Add(-time.Second), 40*time.Millisecond, 0)

	recs, unreadable, err := runlogjsonl.Load(path, func() time.Time { return now })
	if err != nil {
		t.Fatalf("журнал не читается: %v", err)
	}
	if unreadable != 0 {
		t.Fatalf("нечитаемых строк %d", unreadable)
	}
	if len(recs) != 1 {
		t.Fatalf("записей %d, ждали 1", len(recs))
	}
	got := recs[0]
	if want := runs.ToolCommand("search_catalog"); got.Command() != want {
		t.Errorf("команда %q, ждали %q — под другим именем счётчик вызовов её не найдёт", got.Command(), want)
	}
	if name, ok := runs.ToolOf(got.Command()); !ok || name != "search_catalog" {
		t.Errorf("читатель не разобрал имя вызова: %q, %v", name, ok)
	}
	if args := got.Args(); len(args) != 1 || args[0] != "кубернетес" {
		t.Errorf("аргументы %v, ждали [кубернетес]", args)
	}
	if stderr.Len() != 0 {
		t.Errorf("исправная запись ничего не печатает, напечатано: %s", stderr.String())
	}
}

// Отрицательный контроль: сломанный журнал НАЗЫВАЕТСЯ вслух и не роняет ответ.
// Молчащий журнал читается как «вызовов не было» — худший ответ для счётчика,
// заведённого ради вопроса «сколько раз спрашивали».
func TestJournalRecorder_brokenJournalIsNamedNotSwallowed(t *testing.T) {
	// Файл на месте каталога журнала: создать каталог поверх файла нельзя.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "занято")
	if err := writeFile(blocker, "не каталог"); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	r := newJournalRecorder(filepath.Join(blocker, "runs.jsonl"), time.Now, &stderr)
	r.RecordCall("stats", nil, time.Now(), time.Millisecond, 0)

	if !strings.Contains(stderr.String(), "журнал") {
		t.Errorf("о сломанном журнале не сказано ни слова, в stderr: %q", stderr.String())
	}
}
