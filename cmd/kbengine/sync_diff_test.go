package main

import (
	"bytes"
	"strings"
	"testing"
)

// addToLedgerWithAccount кладет расход с указанным счетом — или без него,
// когда счет пустой, как и получилось у владельца.
func addToLedgerWithAccount(t *testing.T, ledger, account string) {
	t.Helper()
	args := []string{
		"fin", "add", "--ledger", ledger, "--amount", "322", "--cat", "Транспорт",
		"--sub", "Такси", "--note", "такси до центра", "--date", "2026-05-02",
	}
	if account != "" {
		args = append(args, "--account", account)
	}
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}
}

// Сухой прогон печатал три числа: «+1 added, 0 modified, -0 removed». Числа
// говорят, сколько строк тронется, и не говорят каких — а решение принимается
// именно по содержимому: та ли это трата, тот ли счет, не поедет ли в книгу
// вторая копия. Владелец назвал образец: diff, как показывает lazygit.
//
// Отсюда построчный вывод: `+` на том, что появится, `-` на том, что уйдет, и
// обе строки подряд там, где запись изменилась.

func TestSyncDryRunShowsWhatChanges(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)

	code, stdout, stderr := sync(t, xlsx, ledger, "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	// Счетчики остаются: они отвечают на «сколько», строки — на «что именно».
	if !strings.Contains(stdout, "1 added") {
		t.Errorf("сводка пропала из вывода:\n%s", stdout)
	}
	if !strings.Contains(stdout, "+") || !strings.Contains(stdout, "из терминала") {
		t.Errorf("не видно, какая именно запись добавится:\n%s", stdout)
	}
}

// Счет расхода в описании строки не показывался вовсе — его печатали только у
// дохода. Ровно этого поля не хватило, когда трата ушла в книгу ничьей: в
// отчете она выглядела полной.
func TestSyncDiffNamesTheAccountOfAnExpense(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedgerWithAccount(t, ledger, "Сбербанк")

	_, stdout, _ := sync(t, xlsx, ledger, "--dry-run")
	if !strings.Contains(stdout, "Сбербанк") {
		t.Errorf("счет расхода не назван в diff:\n%s", stdout)
	}
}

// Расход без счета нельзя показывать так же, как расход со счетом: именно эта
// неразличимость и позволила записи уехать незамеченной.
func TestSyncDiffMarksAnExpenseWithoutAccount(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedgerWithAccount(t, ledger, "")

	_, stdout, _ := sync(t, xlsx, ledger, "--dry-run")
	if !strings.Contains(stdout, "без счета") {
		t.Errorf("расход без счета ничем не помечен:\n%s", stdout)
	}
}

// Цвет — украшение, и оно не должно доезжать до файла или пайпа: тест пишет в
// буфер, то есть ровно в не-терминал, и ESC-последовательностей тут быть не может.
func TestSyncDiffKeepsAnsiOutOfPipes(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)

	_, stdout, _ := sync(t, xlsx, ledger, "--dry-run")
	if strings.Contains(stdout, "\x1b[") {
		t.Errorf("ANSI-раскраска попала в не-терминал:\n%q", stdout)
	}
}
