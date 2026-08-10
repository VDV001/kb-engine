package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
)

// The chat skill updates balances by writing into the workbook's cells with
// openpyxl — the one place it goes around the engine. It does that because the
// engine offered no way to do it. This command is that way; once it exists the
// exception in the skill has nothing left to justify it.
func TestRun_finBalance(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Сбербанк", "--amount", "4321,55"}, &out, &errb); code != 0 {
		t.Fatalf("fin balance exit = %d, stderr = %s", code, errb.String())
	}

	// The new balance is reported back, so the person sees what the file now
	// says rather than only that something was written. Printed the way Money
	// prints everywhere in the engine — with a dot — even though the flag
	// accepts the comma a person types.
	for _, want := range []string{"Сбербанк", "4321.55"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("вывод не содержит %q:\n%s", want, out.String())
		}
	}
	if got := accountBalance(t, book, "Сбербанк"); got != "4321.55" {
		t.Errorf("баланс в книге = %s, ожидалось 4321.55", got)
	}
}

// A bank the sheet does not list is refused, and the refusal names what is
// there. Creating the row instead would invent a word for the vocabulary that
// decides what counts as an account everywhere else in the book.
func TestRun_finBalanceRefusesAnUnknownBank(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "Озон Банк", "--amount", "500"}, &out, &errb)
	if code == 0 {
		t.Fatal("неизвестный банк принят, ожидался отказ")
	}
	for _, want := range []string{"Озон Банк", "Сбербанк"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("отказ не назвал %q:\n%s", want, errb.String())
		}
	}
}

// An amount that does not parse must not reach the workbook. Reporting it as a
// bad amount, next to the flag that carried it, is the difference between a
// fixable mistake and a file to inspect afterwards.
func TestRun_finBalanceRefusesAnUnparsableAmount(t *testing.T) {
	book := workbook(t)
	before := accountBalance(t, book, "Сбербанк")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Сбербанк", "--amount", "много"}, &out, &errb); code == 0 {
		t.Fatal("неразобранная сумма принята, ожидался отказ")
	}
	// The refusal has to be about the amount. A test that only checks the exit
	// code passes just as well when the command does not exist at all — it did,
	// on the RED run.
	if !strings.Contains(errb.String(), "много") {
		t.Errorf("отказ не назвал неразобранную сумму:\n%s", errb.String())
	}
	if after := accountBalance(t, book, "Сбербанк"); after != before {
		t.Errorf("баланс изменился на %s при отказе, был %s", after, before)
	}
}

// Подтверждение оставляет момент, а не только день.
//
// Проверка стоит на живом пути команды, а не на функции записи: экран терминала
// и команда пишут баланс одним вызовом ровно затем, чтобы момент нельзя было
// потерять, выбрав другую поверхность. День при этом остаётся в книге — колонку
// «Обновлено» читает человек.
func TestRun_finBalanceRecordsTheConfirmationMoment(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Сбербанк", "--amount", "4321,55"}, &out, &errb); code != 0 {
		t.Fatalf("fin balance exit = %d, stderr = %s", code, errb.String())
	}

	state, err := balancestate.Load(balancestate.PathNextTo(book))
	if err != nil {
		t.Fatalf("balancestate.Load: %v", err)
	}
	moment, known := state.At("Сбербанк")
	if !known {
		t.Fatal("момент подтверждения не записан — расчёт останется на приблизительном правиле")
	}
	if time.Since(moment) > time.Minute {
		t.Errorf("момент = %s, ожидался момент этого запуска", moment)
	}
}

// accountBalance reads the raw balance cell for a bank off the Счета sheet.
func accountBalance(t *testing.T, path, bank string) string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	rows, err := f.GetRows("Счета")
	if err != nil {
		t.Fatalf("GetRows Счета: %v", err)
	}
	for _, r := range rows {
		if len(r) > 1 && strings.TrimSpace(r[0]) == bank {
			return r[1]
		}
	}
	t.Fatalf("счёт %q не найден", bank)
	return ""
}
