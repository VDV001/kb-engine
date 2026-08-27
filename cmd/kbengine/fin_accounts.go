package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// runFinAccounts prints the names of the accounts the workbook lists, one per
// line, and nothing else.
//
// Зачем отдельная команда, когда есть `fin report`. Её потребитель — не человек,
// а проверка: гейт `scripts/gates/push.sh` собирает список личных имён, чтобы не
// дать им уехать в публичный репозиторий. До этой команды он брал имена из
// ledger и сам объявлял, чего этот источник не видит: «счёт, по которому нет ни
// одной транзакции, в ledger не попадает вовсе — а именно так устроен долговой
// счёт». То есть мимо проверки шло самое чувствительное имя. Замер 26.08: такое
// имя стояло в публичном репозитории 17 раз, гейт молчал, нашлось руками.
//
// ⚠️ ОСТАТКИ НЕ ПЕЧАТАЮТСЯ, и это решение, а не упущение. Гейт зовёт команду при
// каждом push и пишет её вывод в лог; печатай она рядом с именем сумму — сам
// инструмент против утечек стал бы утечкой. Кому нужен остаток, у того есть
// `fin balance` и `fin report`.
//
// ⚠️ Порядок — как в книге, без сортировки. Лист «Счета» человек ведёт руками и
// группирует по смыслу (свои счета, потом заморозка, потом обязательства);
// алфавит эту группировку разрушил бы, а никакой выгоды потребителю не даёт.
func runFinAccounts(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin accounts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "path to Учёт_финансов.xlsx")
	// parseFlags, а не своя ветка на err != nil: у команд движка три ответа, а не
	// два — помощь это успех, ошибка разбора это код 2. Своя ветка возвращала на
	// `--help` двойку, и поймал это не мой тест, а общий
	// TestHelpIsSuccessForEveryFinSubcommand, который проверяет ВСЕ подкоманды
	// из реестра — то есть новая команда попала под него автоматически.
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *from == "" {
		fmt.Fprintln(stderr, "fin accounts: --from обязателен")
		return 2
	}

	// Отказ чтения обязан отличаться от пустого списка: «не смог прочитать» и
	// «счетов нет» — разные ответы, и второй читался бы как разрешение.
	led, err := financexlsx.Read(*from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin accounts: не смог прочитать книгу: %v\n", err)
		return 1
	}

	for _, a := range led.Accounts {
		fmt.Fprintln(stdout, a.Bank())
	}
	return 0
}
