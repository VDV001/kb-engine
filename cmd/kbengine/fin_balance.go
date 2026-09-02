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

	existing, known := balanceOf(*from, *bank)

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
	reportUpdate(reportParams{
		path: *from, bank: *bank, flagCurrency: *currency,
		existing: existing, known: known, money: money,
		currency: cur, rate: accRate, today: today,
	}, stdout)
	return 0
}

// reportParams — что нужно знать, чтобы напечатать одну строку отчёта.
//
// Структурой, а не восемью аргументами: у половины из них один тип, и порядок
// в вызове перепутать легче, чем заметить это потом.
type reportParams struct {
	path, bank, flagCurrency string
	existing                 domain.Account
	known                    bool
	money                    domain.Money
	currency                 domain.Currency
	rate                     domain.Rate
	today                    string
}

// reportUpdate печатает, что стало со счётом.
//
// Отдельной функцией: сама команда уже разбирает флаги, читает книгу, пишет и
// решает три вида отказа — отчёт был в ней четвёртой заботой, и линтер это
// заметил раньше меня.
func reportUpdate(p reportParams, stdout io.Writer) {
	// Счёт называется так, как он записан на листе, а не так, как его набрали:
	// «сбербанк» в отчёте о собственной книге читается как другой счёт.
	name := p.bank
	if p.known && p.existing.Bank() != "" {
		name = p.existing.Bank()
	}
	// Единица берётся у счёта, КАК ОН ЛЕЖИТ В КНИГЕ ПОСЛЕ ЗАПИСИ, а не у
	// флагов: обновляя баланс валютного счёта, --currency обычно не передают —
	// он там уже записан, и молчание отчёта про валюту было бы молчанием про
	// самое важное в строке.
	unit := unitSuffix(p.currency, p.rate)
	if p.flagCurrency == "" {
		if after, ok := balanceOf(p.path, p.bank); ok {
			unit = unitSuffix(after.Currency(), after.Rate())
		}
	}
	before := p.existing.Balance()
	switch {
	case p.known && before.String() == p.money.String():
		fmt.Fprintf(stdout, "%s: %s%s — сумма не изменилась, обновлена дата подтверждения (%s)\n",
			name, p.money, unit, p.today)
	case p.known:
		fmt.Fprintf(stdout, "%s: %s → %s%s (%s)\n", name, before, p.money, unit, p.today)
	default:
		fmt.Fprintf(stdout, "%s: %s%s (%s)\n", name, p.money, unit, p.today)
	}
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
	fmt.Fprintf(stdout, "%s: %s%s — новый счёт на листе «Счета» (%s)\n",
		bank, amount, unitSuffix(currency, rate), time.Now().Format(time.DateOnly))
	return 0
}

// unitSuffix — чем строка отчёта заканчивается у валютного счёта и чем не
// заканчивается у рублёвого.
//
// Рублёвый молчит намеренно: приписка «RUB» к каждой строке — шум, который
// приучает не дочитывать строку до конца, а дочитать её придётся именно у
// валютной. «Курс неизвестен» при этом произносится: пустота на месте курса
// снаружи неотличима от курса, который забыли напечатать.
func unitSuffix(currency domain.Currency, rate domain.Rate) string {
	if currency.IsBase() {
		return ""
	}
	if per, ok := rate.PerUnit(); ok {
		return fmt.Sprintf(" %s по курсу %s", currency.Code(), per)
	}
	return " " + currency.Code() + " (курс неизвестен)"
}

// balanceOf возвращает остаток, записанный на счёте сейчас, и его имя так, как
// оно стоит на листе. Последнее значение — false, когда книгу прочитать не
// удалось или счёта в ней нет: тогда отчёт молчит о прежнем значении, а не
// выдумывает его.
//
// Написание сверяет домен — тем же правилом, которым его сверяет запись. Раньше
// здесь стояло побайтовое равенство, и отчёт не узнавал счёт, который сам же
// только что обновил.
func balanceOf(path, bank string) (domain.Account, bool) {
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		return domain.Account{}, false
	}
	for _, a := range led.Accounts {
		if domain.SameAccountName(a.Bank(), bank) {
			return a, true
		}
	}
	return domain.Account{}, false
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
