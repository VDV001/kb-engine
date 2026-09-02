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
	currency := fs.String("currency", "", "currency code of the account, e.g. USD (empty means the book's own currency)")
	rate := fs.String("rate", "", "how much of the book's currency one unit is worth, e.g. 84,28")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	money, code := requireBalanceArgs(*from, *bank, *amount, stderr)
	if code != 0 {
		return code
	}

	// Прежнее значение читается до записи, чтобы отчёт мог сказать, что именно
	// произошло. Одно сообщение и на «сумма изменилась», и на «сумма уже была
	// такой» — это «выполнено» без содержания: после него никто не приходит
	// проверять, хотя изменилась только дата подтверждения.
	// Валюта и курс разбираются ДО чтения книги и до любой записи: отказ обязан
	// оставить книгу ровно такой, какой она была, а полработы хуже, чем ничего,
	// потому что за ними никто не приходит.
	cur, accRate, code := parseCurrencyAndRate(*currency, *rate, stderr)
	if code != 0 {
		return code
	}

	before, onSheet, known := balanceOf(*from, *bank)

	if *create {
		return createAccount(*from, *bank, money, cur, accRate, stdout, stderr)
	}

	if code := applyCurrency(*from, *bank, *currency, *rate, cur, accRate, stderr); code != 0 {
		return code
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
func createAccount(path, bank string, amount domain.Money, currency domain.Currency, rate domain.Rate, stdout, stderr io.Writer) int {
	if err := financexlsx.AddAccount(path, bank, amount, currency, rate, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin balance: %v\n", err)
		if errors.Is(err, financexlsx.ErrAccountExists) {
			fmt.Fprintf(stderr, "  счёт уже есть — обновить его баланс можно тем же вызовом без --create\n")
		}
		return 1
	}
	// Заведение названо вслух отдельной строкой: «Займ → Коллеге: 3000.00»
	// читается одинаково и когда счёт появился, и когда у него поменялась
	// сумма, а различить их — ровно то, ради чего человек передал флаг.
	// Валюта называется вслух, если она не рублёвая: строка «500.00» у счёта в
	// долларах читается как рубли, и это ровно тот дефект, из-за которого
	// заведена #332.
	unit := ""
	if !currency.IsBase() {
		unit = " " + currency.Code()
		if per, ok := rate.PerUnit(); ok {
			unit += fmt.Sprintf(" по курсу %s", per)
		} else {
			unit += " (курс неизвестен)"
		}
	}
	fmt.Fprintf(stdout, "%s: %s%s — новый счёт на листе «Счета» (%s)\n",
		bank, amount, unit, time.Now().Format(time.DateOnly))
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

// parseCurrencyAndRate разбирает пару флагов и решает, законна ли она.
//
// Курс без валюты — противоречие, а не мелочь: рубль оценивают в рублях только
// по единице, и такая пара почти всегда означает, что человек перепутал флаг.
// Валюта без курса, наоборот, законна: у наличных, полученных подарком, курса
// может не быть вовсе, и выдумывать его значило бы записать замер, которого
// никто не делал.
func parseCurrencyAndRate(code, rawRate string, stderr io.Writer) (domain.Currency, domain.Rate, int) {
	rate := domain.UnknownRate()
	if code == "" {
		if rawRate != "" {
			fmt.Fprintf(stderr, "fin balance: --rate без --currency: у счёта в валюте книги курс всегда единица\n")
			return domain.Currency{}, rate, 2
		}
		return domain.Currency{}, rate, 0
	}
	cur, err := domain.NewCurrency(code)
	if err != nil {
		fmt.Fprintf(stderr, "fin balance: --currency %q: %v\n", code, err)
		return domain.Currency{}, rate, 1
	}
	if rawRate == "" {
		return cur, rate, 0
	}
	per, err := domain.ParseMoney(rawRate)
	if err != nil {
		fmt.Fprintf(stderr, "fin balance: --rate %q: %v\n", rawRate, err)
		return domain.Currency{}, rate, 1
	}
	if rate, err = domain.NewRate(per); err != nil {
		fmt.Fprintf(stderr, "fin balance: --rate %q: %v\n", rawRate, err)
		return domain.Currency{}, rate, 1
	}
	return cur, rate, 0
}

// applyCurrency обновляет валюту счёта, который на листе уже есть.
//
// Вынесено из runFinBalance ради читаемости: сама команда и без того решает
// четыре вопроса — разобрать сумму, завести счёт, обновить остаток, сказать,
// что именно изменилось.
func applyCurrency(path, bank, rawCurrency, rawRate string, currency domain.Currency, rate domain.Rate, stderr io.Writer) int {
	if rawCurrency == "" && rawRate == "" {
		return 0
	}
	if err := financexlsx.SetCurrency(path, bank, currency, rate, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin balance: %v\n", err)
		return 1
	}
	return 0
}

// requireBalanceArgs проверяет обязательные флаги и разбирает сумму.
//
// ParseMoney, а не MoneyFromFloat: это текст, который набрал человек, поэтому
// точность мельче копейки — опечатка, и о ней сообщают, а не округляют её.
func requireBalanceArgs(from, bank, amount string, stderr io.Writer) (domain.Money, int) {
	for _, req := range []struct{ flag, value string }{
		{"--from", from}, {"--bank", bank}, {"--amount", amount},
	} {
		if req.value == "" {
			fmt.Fprintf(stderr, "fin balance: %s is required\n", req.flag)
			return domain.Money{}, 2
		}
	}
	money, err := domain.ParseMoney(amount)
	if err != nil {
		fmt.Fprintf(stderr, "fin balance: --amount %q: %v\n", amount, err)
		return domain.Money{}, 1
	}
	return money, 0
}
