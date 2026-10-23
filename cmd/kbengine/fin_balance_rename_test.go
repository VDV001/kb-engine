package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --rename переносит счёт в другой род без правки ячеек руками: род читается из
// имени, и другого входа, кроме имени, у книги нет. Сумма при этом не нужна —
// деньги не меняются, меняется слово.
func TestRun_finBalanceRenamesAnAccount(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Займ → Коллеге", "--amount", "300", "--create"}, &out, &errb); code != 0 {
		t.Fatalf("заведение счёта: %s", errb.String())
	}
	out.Reset()

	ledger := filepath.Join(filepath.Dir(book), "transactions.jsonl")
	aliases := filepath.Join(filepath.Dir(book), "finance-aliases.json")
	if err := os.WriteFile(aliases, []byte(`{"accounts": {"займ": "Займ → Коллеге"}, "places": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code := run([]string{"fin", "balance", "--from", book, "--ledger", ledger, "--bank", "займ→коллеге", "--rename", "Коллега"}, &out, &errb)
	if code != 0 {
		t.Fatalf("fin balance --rename exit = %d, stderr = %s", code, errb.String())
	}
	// Отчёт называет обе стороны так, как они записаны на листе: «т банк» в
	// отчёте о собственной книге читался бы как третий счёт.
	for _, want := range []string{"Займ → Коллеге → Коллега"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("вывод не содержит %q:\n%s", want, out.String())
		}
	}
	if got := accountBalanceValue(t, book, "Коллега"); got != 300 {
		t.Errorf("баланс после переименования = %v, ожидалось 300", got)
	}
	// Словарь быстрого ввода переписан вслед за книгой (правило 26).
	raw, err := os.ReadFile(aliases)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"Коллега"`) || strings.Contains(string(raw), "Займ → Коллеге") {
		t.Errorf("словарь не переписан:\n%s", raw)
	}
}

// Без журнала нельзя проверить записи, ещё не перенесённые в книгу, и словарь,
// который лежит рядом с журналом.
func TestRun_finBalanceRenameNeedsTheLedger(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "Сбербанк", "--rename", "Сбер"}, &out, &errb)
	if code == 0 {
		t.Fatal("--rename без --ledger принят")
	}
	if !strings.Contains(errb.String(), "--ledger") {
		t.Errorf("отказ не назвал флаг:\n%s", errb.String())
	}
}

// Запись журнала, ещё не синхронизированная с книгой, ссылается на счёт по
// имени: переименование оставило бы её с несуществующим счётом, и следующая
// синхронизация отказала бы.
func TestRun_finBalanceRenameRefusesWhenTheLedgerNamesTheAccount(t *testing.T) {
	book := workbook(t)
	ledger := filepath.Join(filepath.Dir(book), "transactions.jsonl")
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Займ → Коллеге", "--amount", "300", "--create"}, &out, &errb); code != 0 {
		t.Fatalf("заведение: %s", errb.String())
	}
	if code := run([]string{"fin", "add", "--ledger", ledger, "--kind", "expense", "--date", "2026-08-01",
		"--cat", "Еда", "--amount", "100", "--account", "Займ → Коллеге"}, &out, &errb); code != 0 {
		t.Fatalf("fin add: %s", errb.String())
	}
	errb.Reset()

	code := run([]string{"fin", "balance", "--from", book, "--ledger", ledger, "--bank", "Займ → Коллеге", "--rename", "Коллега"}, &out, &errb)
	if code == 0 {
		t.Fatal("переименован счёт, на который ссылается журнал")
	}
	if accountBalanceValue(t, book, "Займ → Коллеге") != 300 {
		t.Error("отказ изменил книгу")
	}
}

// Переименование и запись суммы — разные намерения. Вместе они делали бы два
// дела одной командой, и отказ одного оставлял бы полкниги сделанной.
func TestRun_finBalanceRenameRefusesAnAmount(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "Сбербанк", "--rename", "Сбер", "--amount", "5"}, &out, &errb)
	if code == 0 {
		t.Fatal("--rename вместе с --amount принят, ожидался отказ")
	}
	if !strings.Contains(errb.String(), "--rename") {
		t.Errorf("отказ не назвал флаг:\n%s", errb.String())
	}
}
