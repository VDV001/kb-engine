package main

import (
	"bytes"
	"strconv"
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
	// Сравнение числом, а не строкой: сколько знаков после запятой покажет
	// ячейка, решает её формат, и в фикстуре его нет. Наследование формата —
	// вопрос отдельного теста в адаптере, здесь проверяется записанная сумма.
	if got := accountBalanceValue(t, book, "Долг → Отец"); got != 3000 {
		t.Errorf("баланс в книге = %v, ожидалось 3000", got)
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
	if got := accountBalanceValue(t, book, "Сбербанк"); got == 500 {
		t.Error("баланс существующего счёта переписан отказавшей командой")
	}
}

// accountBalanceValue reads a balance as a number, leaving how many decimals
// the cell shows to the cell's own format.
func accountBalanceValue(t *testing.T, path, bank string) float64 {
	t.Helper()
	raw := accountBalance(t, path, bank)
	v, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
	if err != nil {
		t.Fatalf("баланс %q не число: %v", raw, err)
	}
	return v
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
