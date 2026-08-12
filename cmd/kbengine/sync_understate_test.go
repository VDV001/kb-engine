package main

import (
	"bytes"

	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
)

// Синхронизация называет траты, которые занизят расчётный остаток.
//
// Расчёт вычитает из подтверждённого числа всё, записанное после момента
// подтверждения. Трата, ДАТИРОВАННАЯ днём подтверждения, попадает под это
// правило, даже если по банку она прошла раньше — и тогда вычитается второй
// раз. Арифметика при этом верна по своему замыслу: движок обещал занижать и
// никогда не завышать. Неверно молчание — человек видит расхождение с банком и
// не знает, откуда оно.
//
// Проверка стоит на `fin sync`, потому что он единственный видит обе стороны:
// у `fin add` книги нет, а момент подтверждения лежит рядом с ней.
func TestRun_finSyncNamesExpensesThatUnderstateTheBalance(t *testing.T) {
	book := workbook(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")

	initSync(t, book, ledger)
	confirmBalance(t, book, "Сбербанк", "1000,00")

	// Трата сегодняшним днём, записанная ПОСЛЕ подтверждения.
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--kind", "expense",
		"--cat", "Транспорт", "--amount", "105", "--account", "Сбербанк"}, &out, &errb); code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}

	out.Reset()
	if code := run([]string{"fin", "sync", "--from", book, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync exit = %d, stderr = %s", code, errb.String())
	}

	for _, want := range []string{"105", "Сбербанк", "подтвердите"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("отчёт не содержит %q:\n%s", want, out.String())
		}
	}
}

// Спорных трат нет — синхронизация об этом молчит. Предупреждение, которое
// приходит всегда, перестают читать за неделю.
func TestRun_finSyncSilentWhenNothingUnderstates(t *testing.T) {
	book := workbook(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")

	initSync(t, book, ledger)
	confirmBalance(t, book, "Сбербанк", "1000,00")

	// Трата вчерашняя: день не совпадает с днём подтверждения, спора нет.
	yesterday := time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--kind", "expense", "--date", yesterday,
		"--cat", "Транспорт", "--amount", "42", "--account", "Сбербанк"}, &out, &errb); code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}

	out.Reset()
	if code := run([]string{"fin", "sync", "--from", book, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync exit = %d, stderr = %s", code, errb.String())
	}

	if strings.Contains(out.String(), "подтвердите") {
		t.Errorf("предупреждение пришло там, где спора нет:\n%s", out.String())
	}
}

// confirmBalance записывает остаток счёта через ту же команду, что и человек —
// вместе с моментом подтверждения, который эта команда кладёт рядом с книгой.
//
// Момент отодвигается на час назад намеренно. Команда ставит «сейчас», а тест
// пишет трату следующей строкой, и обе укладываются в одну миллисекунду; момент
// записи читается из ULID, у которого точность как раз миллисекунда, и трата
// оказывается записанной «до» подтверждения на доли миллисекунды. У человека
// между этими двумя действиями проходят минуты, и тест моделирует это, а не
// скорость машины.
func confirmBalance(t *testing.T, book, bank, amount string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", book, "--bank", bank, "--amount", amount}, &out, &errb); code != 0 {
		t.Fatalf("fin balance exit = %d, stderr = %s", code, errb.String())
	}
	if err := balancestate.Record(balancestate.PathNextTo(book), bank, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// initSync создаёт baseline трёхстороннего сравнения. Без него первая же
// синхронизация видит расхождение двух сторон и отказывается решать, какая
// права — правильное поведение, но не то, что проверяют эти тесты.
func initSync(t *testing.T, book, ledger string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--from", book, "--ledger", ledger, "--init"}, &out, &errb); code != 0 {
		t.Fatalf("fin sync --init exit = %d, stderr = %s", code, errb.String())
	}
}
