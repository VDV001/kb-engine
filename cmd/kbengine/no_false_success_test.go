package main

import (
	"bytes"
	"strings"
	"testing"
)

// Гейт на класс «выполнено без содержания».
//
// Повод — измерение, а не предположение: прогон всех пишущих команд в сценарии
// «эффект ноль» нашёл четыре места, где команда рапортовала успехом, ничего не
// изменив. Два из них были написаны в тот же день, что и проверка, — то есть
// правило «не проглатывать» держалось на внимании, а внимание кончается.
//
// Правило, которое проверяет этот тест: команда, повторённая с теми же
// значениями, обязана либо вернуть ненулевой код, либо сказать вслух, что
// ничего не изменилось. Просто напечатать то же самое сообщение об успехе она
// не имеет права: после «выполнено» никто не приходит проверять.
//
// Новую пишущую команду добавляйте сюда же строкой в таблицу. Тест намеренно
// прогоняет живой путь целиком, а не отдельные функции: обойти проверку,
// поставив её в usecase, легко — экран или команда просто не позовут её.

func TestNoCommandReportsSuccessWithoutChanging(t *testing.T) {
	type probe struct {
		name string
		// setup готовит площадку и возвращает аргументы команды.
		setup func(t *testing.T) []string
	}

	probes := []probe{
		{
			name: "set: то же состояние",
			setup: func(t *testing.T) []string {
				path := baseWithCatalog(t)
				return []string{"set", "--catalog", path, "--ids", "1", "--lifecycle", "active"}
			},
		},
		{
			name: "fin edit: те же значения",
			setup: func(t *testing.T) []string {
				_, ledger := pairedLedger(t)
				addToLedgerWithAccount(t, ledger, "Сбербанк")
				return []string{"fin", "edit", "--ledger", ledger, "--id", lastID(t, ledger),
					"--account", "Сбербанк"}
			},
		},
		{
			name: "fin add: повтор траты",
			setup: func(t *testing.T) []string {
				_, ledger := pairedLedger(t)
				addToLedgerWithAccount(t, ledger, "Сбербанк")
				return []string{"fin", "add", "--ledger", ledger, "--amount", "322",
					"--cat", "Транспорт", "--sub", "Такси", "--note", "такси до центра",
					"--date", "2026-05-02", "--account", "Сбербанк"}
			},
		},
		{
			name: "fin sync: обе стороны согласны",
			setup: func(t *testing.T) []string {
				xlsx, ledger := pairedLedger(t)
				return []string{"fin", "sync", "--from", xlsx, "--ledger", ledger}
			},
		},
		{
			// Заведение счёта, который уже есть: успех здесь означал бы, что на
			// листе стало две строки об одном счёте — а обе выглядели бы
			// одинаково правдоподобно.
			name: "fin balance --create: счёт уже на листе",
			setup: func(t *testing.T) []string {
				xlsx, _ := pairedLedger(t)
				return []string{"fin", "balance", "--from", xlsx, "--bank", "Сбербанк",
					"--amount", "500", "--create"}
			},
		},
		{
			// Удаление без подтверждения: команда обязана сказать, что ничего не
			// сделала. Молчаливый успех здесь читался бы как «запись удалена», и
			// человек узнал бы правду только из отчёта через месяц.
			name: "fin delete: подтверждения не было",
			setup: func(t *testing.T) []string {
				_, ledger := pairedLedger(t)
				addToLedgerWithAccount(t, ledger, "Сбербанк")
				return []string{"fin", "delete", "--ledger", ledger, "--id", lastID(t, ledger)}
			},
		},
	}

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			args := p.setup(t)

			var out, errb bytes.Buffer
			code := run(args, &out, &errb)

			if code != 0 {
				return // отказ — законный способ не соврать
			}
			said := out.String() + errb.String()
			if !saysNothingChanged(said) {
				t.Errorf("команда вернула 0 и не сказала, что ничего не изменилось:\n%s", said)
			}
		})
	}
}

// saysNothingChanged ищет признание в человеческом тексте. Список формулировок,
// а не одна: сообщения пишутся по-разному, и требовать точную строку значило бы
// чинить тест при каждой правке текста.
func saysNothingChanged(s string) bool {
	for _, mark := range []string{
		"nothing to do", "нечего", "не изменил", "уже так", "уже в этом состоянии",
		"already", "no change", "не удалено",
	} {
		if strings.Contains(strings.ToLower(s), strings.ToLower(mark)) {
			return true
		}
	}
	return false
}
