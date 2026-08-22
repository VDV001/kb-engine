package main

import (
	"bytes"
	"strings"
	"testing"
)

// Двадцать шесть отказов `fin` из девяноста четырёх прогонов — перенос пары
// флагов с соседней подкоманды: `--from` есть у balance/sync/import и нет у
// list/report, `--ledger` есть у восьми и нет у balance. Внутри это логично
// (balance пишет на лист «Счета», report читает журнал), снаружи не сказано
// нигде, и сообщение называет, чего нет, не называя, что есть вместо.
func TestFinNamesTheSubcommandThatHasTheFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fin", "report", "--from", "книга.xlsx"}, &out, &errb)

	said := out.String() + errb.String()
	if code == 0 {
		t.Fatalf("report принял чужой флаг:\n%s", said)
	}
	for _, want := range []string{"balance", "import", "sync"} {
		if !strings.Contains(said, want) {
			t.Errorf("подсказка не назвала %q:\n%s", want, said)
		}
	}
}

// Отрицательный контроль: флаг, которого нет нигде, подсказки не заслуживает.
// Без этой половины подсказка стала бы шумом на каждой опечатке.
func TestFinStaysSilentForAFlagNobodyHas(t *testing.T) {
	var out, errb bytes.Buffer
	// Имя латиницей и намеренно: кириллическое имя не проходит регулярку
	// разбора сообщения, и проверка молчала бы независимо от починки — то есть
	// проходила бы по неверной причине. Поймано подсадкой «подсказывать всегда».
	run([]string{"fin", "sync", "--nosuchflag", "x"}, &out, &errb)

	said := out.String() + errb.String()
	if strings.Contains(said, "принимают") {
		t.Errorf("подсказка выдумана для флага, которого нет ни у кого:\n%s", said)
	}
}

// И вторая половина того же: удачный разбор подсказок не печатает.
func TestFinSaysNothingExtraOnSuccess(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"fin", "--help"}, &out, &errb)

	if said := out.String() + errb.String(); strings.Contains(said, "принимают") {
		t.Errorf("подсказка вылезла на успешном пути:\n%s", said)
	}
}
