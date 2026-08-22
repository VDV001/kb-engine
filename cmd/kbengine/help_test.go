package main

import (
	"bytes"
	"strings"
	"testing"
)

// Просьба о помощи — это успех.
//
// Повод замеренный: в журнале прогонов пять отказов `fin` из тридцати одного
// оказались вызовами `--help`, то есть находка «команда падает в трети
// прогонов» была завышена на шестую часть самим устройством помощи. Отдельно
// от журнала: скрипт под `set -e`, зовущий `kbengine ... --help`, падает.
//
// Множество команд берётся из реестра `commands`, а не из вписанного сюда
// списка: список молча разошёлся бы с кодом при первой новой команде — тот же
// класс, из-за которого тесты «каждый» проверяли подмножество полей.

func TestHelpIsSuccessForEveryCommand(t *testing.T) {
	for name := range commands {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{name, "--help"}, &out, &errb)

			said := out.String() + errb.String()
			if code != 0 {
				t.Errorf("%s --help вернул %d, а помощь это не ошибка:\n%s", name, code, said)
			}
			if !strings.Contains(strings.ToLower(said), "usage") {
				t.Errorf("%s --help не напечатал помощь:\n%s", name, said)
			}
		})
	}
}

// Верхний уровень: `--help` не команда, поэтому диспетчер обязан узнать его
// раньше, чем скажет «неизвестная команда». Сейчас говорит именно это.
func TestTopLevelHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{arg}, &out, &errb)

			said := out.String() + errb.String()
			if code != 0 {
				t.Errorf("kbengine %s вернул %d:\n%s", arg, code, said)
			}
			if !strings.Contains(said, "usage: kbengine") {
				t.Errorf("kbengine %s не напечатал список команд:\n%s", arg, said)
			}
		})
	}
}
