package main

import (
	"bytes"
	"strings"
	"testing"
)

// --rename переносит счёт в другой род без правки ячеек руками: род читается из
// имени, и другого входа, кроме имени, у книги нет. Сумма при этом не нужна —
// деньги не меняются, меняется слово.
func TestRun_finBalanceRenamesAnAccount(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "т банк", "--rename", "Т-Банк карта"}, &out, &errb)
	if code != 0 {
		t.Fatalf("fin balance --rename exit = %d, stderr = %s", code, errb.String())
	}
	// Отчёт называет обе стороны так, как они записаны на листе: «т банк» в
	// отчёте о собственной книге читался бы как третий счёт.
	for _, want := range []string{"Т-Банк", "→", "Т-Банк карта"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("вывод не содержит %q:\n%s", want, out.String())
		}
	}
	accountBalanceValue(t, book, "Т-Банк карта")
}

// Переименование и запись суммы — разные намерения. Вместе они делали бы два
// дела одной командой, и отказ одного оставлял бы полкниги сделанной.
func TestRun_finBalanceRenameRefusesAnAmount(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "Т-Банк", "--rename", "Т-Банк карта", "--amount", "5"}, &out, &errb)
	if code == 0 {
		t.Fatal("--rename вместе с --amount принят, ожидался отказ")
	}
	if !strings.Contains(errb.String(), "--rename") {
		t.Errorf("отказ не назвал флаг:\n%s", errb.String())
	}
}
