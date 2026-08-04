package main

import (
	"bytes"
	"strings"
	"testing"
)

// --create is how a person says «this account is new», and saying it is the
// whole point: without the flag an unknown name stays a typo, because the Счета
// sheet is the vocabulary the rest of the book reads back.
func TestRun_finBalanceCreatesANewAccount(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "Долг → Отец", "--amount", "3000", "--create"}, &out, &errb)
	if code != 0 {
		t.Fatalf("fin balance --create exit = %d, stderr = %s", code, errb.String())
	}

	// The report says the row is new. «Долг → Отец: 3000.00» alone reads the
	// same as an update, and the difference is exactly what the person asked to
	// confirm by passing the flag.
	for _, want := range []string{"Долг → Отец", "3000.00", "новый счёт"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("вывод не содержит %q:\n%s", want, out.String())
		}
	}
	if got := accountBalance(t, book, "Долг → Отец"); got != "3000.00" {
		t.Errorf("баланс в книге = %s, ожидалось 3000.00", got)
	}
}

// The flag is not a synonym for «write it anyway». An account that is already
// there means the caller's assumption is wrong, and the difference matters: one
// of the two rows would silently stop being counted by the person reading them.
func TestRun_finBalanceCreateRefusesAnAccountThatExists(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "balance", "--from", book, "--bank", "сбербанк", "--amount", "500", "--create"}, &out, &errb)
	if code == 0 {
		t.Fatal("существующий счёт заведён повторно, ожидался отказ")
	}
	// The refusal names the spelling that is already there and points at the
	// command that does what the person probably meant.
	for _, want := range []string{"Сбербанк", "--create"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("отказ не назвал %q:\n%s", want, errb.String())
		}
	}
	if got := accountBalance(t, book, "Сбербанк"); got == "500.00" {
		t.Error("баланс существующего счёта переписан отказавшей командой")
	}
}

// Without the flag nothing changes about how an unknown name is treated: the
// refusal that has protected the vocabulary until now has to keep protecting it,
// and it names the flag rather than leaving the person to guess.
func TestRun_finBalanceWithoutCreateStillRefusesAnUnknownBank(t *testing.T) {
	book := workbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", "Долг → Отец", "--amount", "3000"}, &out, &errb); code == 0 {
		t.Fatal("неизвестный счёт записан без --create")
	}
	if !strings.Contains(errb.String(), "--create") {
		t.Errorf("отказ не назвал флаг, которым счёт заводится:\n%s", errb.String())
	}
}
