package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Удаление записи через движок, а не руками в файле. Ручная правка журнала —
// ровно та дверь, через которую в книгу однажды попала строка без id, после чего
// `fin sync` отказывал целиком.

func finDelete(t *testing.T, ledger, stdin string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"fin", "delete", "--ledger", ledger}, extra...)
	code = runWithStdin(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRun_finDelete_removesTheEntry(t *testing.T) {
	_, ledger := pairedLedger(t)
	before := readLedgerFile(t, ledger)
	id := lastID(t, ledger)

	code, stdout, stderr := finDelete(t, ledger, "", "--id", id, "--yes")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	// Запись называется в выводе: удаление денег не должно происходить молча.
	if !strings.Contains(stdout, id) {
		t.Errorf("вывод %q не называет удалённую запись", stdout)
	}
	after := readLedgerFile(t, ledger)
	if len(after) != len(before)-1 {
		t.Fatalf("строк стало %d, было %d — ожидалась ровно одна удалённая", len(after), len(before))
	}
	if ledgerFileHas(t, ledger, id) {
		t.Errorf("запись %s осталась в файле", id)
	}
	// Порядок остальных не тронут: удаление — это не повод пересобрать файл.
	var kept []string
	for _, line := range before {
		if !strings.Contains(line, id) {
			kept = append(kept, line)
		}
	}
	for i := range kept {
		if kept[i] != after[i] {
			t.Fatalf("строка %d изменилась:\n было: %s\nстало: %s", i, kept[i], after[i])
		}
	}
}

// Ошибка в аргументе не должна ничего писать: id набирают руками, и промах по
// букве обязан кончаться отказом, а не удалением соседней записи.
func TestRun_finDelete_refusesUnknownID(t *testing.T) {
	_, ledger := pairedLedger(t)
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	code, _, stderr := finDelete(t, ledger, "", "--id", "01ТАКОГОНЕТ", "--yes")
	if code == 0 {
		t.Fatalf("exit = 0, ожидался отказ")
	}
	if !strings.Contains(stderr, "01ТАКОГОНЕТ") {
		t.Errorf("отказ %q не называет id", stderr)
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("файл изменился при отказе")
	}
}

// Подтверждение — не формальность: сначала показывается сама запись, потом
// задаётся вопрос. Ответ «нет» обязан оставить файл нетронутым.
func TestRun_finDelete_asksBeforeDeleting(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		deleted bool
	}{
		{name: "отказ", answer: "n\n", deleted: false},
		{name: "пустой ответ — тоже отказ", answer: "\n", deleted: false},
		{name: "согласие", answer: "y\n", deleted: true},
		{name: "согласие по-русски", answer: "да\n", deleted: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ledger := pairedLedger(t)
			id := lastID(t, ledger)
			before := len(readLedgerFile(t, ledger))

			code, stdout, stderr := finDelete(t, ledger, tc.answer, "--id", id)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr)
			}
			// Запись показывается ДО вопроса, иначе подтверждать нечего.
			if !strings.Contains(stdout, id) {
				t.Errorf("вывод %q не показал запись перед вопросом", stdout)
			}
			after := len(readLedgerFile(t, ledger))
			if tc.deleted && after != before-1 {
				t.Errorf("строк %d, было %d — запись не удалена при согласии", after, before)
			}
			if !tc.deleted {
				if after != before {
					t.Errorf("строк %d, было %d — запись удалена при отказе", after, before)
				}
				if !strings.Contains(stdout, "не удалено") {
					t.Errorf("вывод %q не говорит, что ничего не сделано", stdout)
				}
			}
		})
	}
}
