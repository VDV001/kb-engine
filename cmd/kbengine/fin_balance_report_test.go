package main

import (
	"bytes"
	"strings"
	"testing"
)

// «Выполнено» — самый дорогой неверный ответ: после него никто не приходит
// проверять. Команда балансов печатала одно и то же сообщение и когда меняла
// сумму, и когда сумма уже была такой — то есть отчитывалась об изменении,
// которого не произошло.
func TestRun_finBalance_saysWhenTheAmountDidNotChange(t *testing.T) {
	xlsx := workbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "balance", "--from", xlsx, "--bank", "Сбербанк", "--amount", "2500,75"}, &out, &errb); code != 0 {
		t.Fatalf("первая запись: exit = %d, stderr = %s", code, errb.String())
	}
	first := out.String()

	out.Reset()
	errb.Reset()
	if code := run([]string{"fin", "balance", "--from", xlsx, "--bank", "Сбербанк", "--amount", "2500,75"}, &out, &errb); code != 0 {
		t.Fatalf("повтор: exit = %d, stderr = %s", code, errb.String())
	}
	second := out.String()

	if first == second {
		t.Errorf("повтор той же суммы отчитался так же, как настоящая правка:\n%q", second)
	}
	if !strings.Contains(second, "не изменил") {
		t.Errorf("не сказано, что сумма осталась прежней: %q", second)
	}
}
