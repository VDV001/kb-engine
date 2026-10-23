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
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Займ → Коллеге", "--amount", "300", "--create"}, &out, &errb); code != 0 {
		t.Fatalf("заведение счёта: %s", errb.String())
	}
	out.Reset()

	code := run([]string{"fin", "balance", "--from", book, "--bank", "займ→коллеге", "--rename", "Коллега"}, &out, &errb)
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
