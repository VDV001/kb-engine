package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Проверка живого пути команды: usecase может быть сколь угодно прав, но
// человек читает то, что напечатано.
func TestFinSpelling_namesBothFormsAndTheirCounts(t *testing.T) {
	_, ledger := pairedLedger(t)
	// Суммы разные: одинаковые записи движок законно отвергает как повтор, и
	// фикстура из трёх одинаковых трат просто не собралась бы.
	addPlace(t, ledger, "Монетка", "101")
	addPlace(t, ledger, "Монетка", "102")
	addPlace(t, ledger, "Монетка", "103")
	// Четвёртая запись правится в файле, а не пишется командой: `fin add` сам
	// приводит написание к уже известному (защита с v0.12.0), и через него
	// расхождение просто не завести. В живом журнале такие строки и лежат —
	// они записаны до того, как защита появилась.
	addPlace(t, ledger, "Монетка", "104")
	rewriteLastPlace(t, ledger, "монетка")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "spelling", "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("код = %d, ждали 0 (stderr: %s)", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"Монетка", "монетка", "3", "1"} {
		if !strings.Contains(got, want) {
			t.Errorf("в выводе нет %q:\n%s", want, got)
		}
	}
}

// «Расхождений не найдено» и молчание — разные ответы: молчание неотличимо от
// непрогнанной проверки.
func TestFinSpelling_saysWhenNothingIsWrong(t *testing.T) {
	_, ledger := pairedLedger(t)
	addPlace(t, ledger, "Монетка", "101")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "spelling", "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("код = %d, ждали 0 (stderr: %s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "расхождений не найдено") {
		t.Errorf("чистый прогон промолчал вместо ответа:\n%s", out.String())
	}
}

// addPlace записывает трату в указанном месте на указанную сумму.
func addPlace(t *testing.T, ledger, place, amount string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run([]string{
		"fin", "add", "--ledger", ledger, "--amount", amount,
		"--cat", "Еда", "--sub", "Продукты", "--place", place,
		"--date", "2026-05-02", "--account", "Сбербанк",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("fin add %s %s: код %d, stderr %s", place, amount, code, errb.String())
	}
}

// rewriteLastPlace меняет место в последней строке журнала, минуя движок:
// так в живом файле и появились расхождения — до того, как запись научилась
// приводить написание к известному.
func rewriteLastPlace(t *testing.T, ledger, place string) {
	t.Helper()
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	last := len(lines) - 1
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[last]), &rec); err != nil {
		t.Fatalf("decode last line: %v", err)
	}
	rec["place"] = place
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	lines[last] = string(out)
	if err := os.WriteFile(ledger, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
}
