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

// Отрицательный контроль к предыдущим двум: починка «помощь это успех» не имеет
// права превратиться в «любой разбор флагов это успех». Без этого случая гейт
// прошёл бы на parseFlags, возвращающем ноль всегда.
func TestUnknownFlagIsStillAnError(t *testing.T) {
	for name := range commands {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{name, "--этого-флага-нет"}, &out, &errb)

			if code == 0 {
				t.Errorf("%s принял выдуманный флаг и отчитался успехом:\n%s",
					name, out.String()+errb.String())
			}
		})
	}
}

// Покрытие подкоманд fin — backfill, а не TDD: их починил тот же parseFlags,
// что и команды верхнего уровня, и на момент написания этого теста они уже
// зелёные. Держим его, потому что реестр finCommands растёт, а помощь у новой
// подкоманды иначе никто не спросит.
func TestHelpIsSuccessForEveryFinSubcommand(t *testing.T) {
	for name := range finCommands {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{"fin", name, "--help"}, &out, &errb)

			said := out.String() + errb.String()
			if code != 0 {
				t.Errorf("fin %s --help вернул %d:\n%s", name, code, said)
			}
			if !strings.Contains(strings.ToLower(said), "usage") {
				t.Errorf("fin %s --help не напечатал помощь:\n%s", name, said)
			}
		})
	}
}

// Тот же класс, что и помощь: `--version` не команда, и до этой проверки
// диспетчер отвечал на неё «unknown command» с кодом 2. Спрашивают её первой
// на незнакомом бинаре и первой же в скриптах установки, где под `set -e`
// ненулевой код роняет весь скрипт.
//
// Ответ обязан совпадать с `kbengine version` дословно: два способа спросить
// одно и то же не имеют права разойтись — это ровно то, ради чего buildInfo
// одна на процесс.
func TestTopLevelVersionMatchesTheCommand(t *testing.T) {
	var want, wantErr bytes.Buffer
	if code := run([]string{"version"}, &want, &wantErr); code != 0 {
		t.Fatalf("kbengine version вернул %d:\n%s", code, want.String()+wantErr.String())
	}

	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{arg}, &out, &errb)

			said := out.String() + errb.String()
			if code != 0 {
				t.Errorf("kbengine %s вернул %d, а вопрос о версии не ошибка:\n%s", arg, code, said)
			}
			if out.String() != want.String() {
				t.Errorf("kbengine %s напечатал %q, а `kbengine version` — %q",
					arg, out.String(), want.String())
			}
		})
	}
}

// Отрицательный контроль: узнавание `--version` не имеет права превратиться в
// «любой флаг верхнего уровня это успех».
func TestTopLevelUnknownFlagIsStillAnError(t *testing.T) {
	for _, arg := range []string{"--этого-флага-нет", "-x", "версия"} {
		t.Run(arg, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{arg}, &out, &errb); code == 0 {
				t.Errorf("kbengine %s отчитался успехом:\n%s", arg, out.String()+errb.String())
			}
		})
	}
}
