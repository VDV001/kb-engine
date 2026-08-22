package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// runFinBalance records a new balance for one account on the workbook's Счета
// sheet.
//
// Balances live in the workbook rather than the ledger: they are a snapshot of
// what a bank says today, not a transaction, and nothing in the ledger produces
// them. That is why this command takes --from and not --ledger.
func runFinBalance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin balance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "path to Учёт_финансов.xlsx")
	bank := fs.String("bank", "", "account name as the Счета sheet spells it")
	amount := fs.String("amount", "", "new balance, e.g. 4321,55")
	create := fs.Bool("create", false, "add the account to the Счета sheet (it must not be there yet)")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	for _, req := range []struct{ flag, value string }{
		{"--from", *from}, {"--bank", *bank}, {"--amount", *amount},
	} {
		if req.value == "" {
			fmt.Fprintf(stderr, "fin balance: %s is required\n", req.flag)
			return 2
		}
	}

	// ParseMoney, not MoneyFromFloat: this is text a person typed, so more
	// precision than a kopeck is a typo and gets reported rather than rounded.
	money, err := domain.ParseMoney(*amount)
	if err != nil {
		fmt.Fprintf(stderr, "fin balance: --amount %q: %v\n", *amount, err)
		return 1
	}

	// Прежнее значение читается до записи, чтобы отчёт мог сказать, что именно
	// произошло. Одно сообщение и на «сумма изменилась», и на «сумма уже была
	// такой» — это «выполнено» без содержания: после него никто не приходит
	// проверять, хотя изменилась только дата подтверждения.
	before, onSheet, known := balanceOf(*from, *bank)

	if *create {
		return createAccount(*from, *bank, money, stdout, stderr)
	}

	if err := financexlsx.SetBalance(*from, *bank, money, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin balance: %v\n", err)
		// Незнакомое имя — это либо опечатка, либо счёт, которого ещё нет.
		// Движок не может решить, какое из двух, но обязан назвать выход из
		// второго случая: иначе человек ищет его вне движка, а вне движка
		// единственный способ — писать в ячейки мимо него.
		if errors.Is(err, financexlsx.ErrUnknownAccount) {
			fmt.Fprintf(stderr, "  если это новый счёт, а не опечатка — тот же вызов с --create заведёт его\n")
		}
		return 1
	}

	today := time.Now().Format(time.DateOnly)
	// Счёт называется так, как он записан на листе, а не так, как его набрали:
	// «сбербанк» в отчёте о собственной книге читается как другой счёт.
	name := *bank
	if onSheet != "" {
		name = onSheet
	}
	switch {
	case known && before.String() == money.String():
		fmt.Fprintf(stdout, "%s: %s — сумма не изменилась, обновлена дата подтверждения (%s)\n",
			name, money, today)
	case known:
		fmt.Fprintf(stdout, "%s: %s → %s (%s)\n", name, before, money, today)
	default:
		fmt.Fprintf(stdout, "%s: %s (%s)\n", name, money, today)
	}
	return 0
}

// createAccount заводит счёт, которого на листе «Счета» ещё нет.
//
// Отдельная функция, а не ветка внутри команды: заведение и правка баланса —
// разные намерения, и читаются они врозь.
func createAccount(path, bank string, amount domain.Money, stdout, stderr io.Writer) int {
	if err := financexlsx.AddAccount(path, bank, amount, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin balance: %v\n", err)
		if errors.Is(err, financexlsx.ErrAccountExists) {
			fmt.Fprintf(stderr, "  счёт уже есть — обновить его баланс можно тем же вызовом без --create\n")
		}
		return 1
	}
	// Заведение названо вслух отдельной строкой: «Займ → Коллеге: 3000.00»
	// читается одинаково и когда счёт появился, и когда у него поменялась
	// сумма, а различить их — ровно то, ради чего человек передал флаг.
	fmt.Fprintf(stdout, "%s: %s — новый счёт на листе «Счета» (%s)\n",
		bank, amount, time.Now().Format(time.DateOnly))
	return 0
}

// balanceOf возвращает остаток, записанный на счёте сейчас, и его имя так, как
// оно стоит на листе. Последнее значение — false, когда книгу прочитать не
// удалось или счёта в ней нет: тогда отчёт молчит о прежнем значении, а не
// выдумывает его.
//
// Написание сверяет домен — тем же правилом, которым его сверяет запись. Раньше
// здесь стояло побайтовое равенство, и отчёт не узнавал счёт, который сам же
// только что обновил.
func balanceOf(path, bank string) (domain.Money, string, bool) {
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		return domain.Money{}, "", false
	}
	for _, a := range led.Accounts {
		if domain.SameAccountName(a.Bank(), bank) {
			return a.Balance(), a.Bank(), true
		}
	}
	return domain.Money{}, "", false
}
