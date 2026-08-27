package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financevocab"
	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/oklog/ulid/v2"
)

// runFin dispatches the ledger subcommands.
// finCommands maps a fin verb to its handler. Реестр, а не switch: строка
// помощи собирается из его ключей, поэтому новая подкоманда не может оказаться
// работающей и неназванной — до этого список в usage поддерживался руками.
var finCommands = map[string]func(args []string, stdin io.Reader, stdout, stderr io.Writer) int{
	"import":   finWithoutStdin(runFinImport),
	"add":      finWithoutStdin(runFinAdd),
	"edit":     finWithoutStdin(runFinEdit),
	"delete":   runFinDelete,
	"accounts": finWithoutStdin(runFinAccounts),
	"balance":  finWithoutStdin(runFinBalance),
	"list":     finWithoutStdin(runFinList),
	"report":   finWithoutStdin(runFinReport),
	"spelling": finWithoutStdin(runFinSpelling),
	"sync":     finWithoutStdin(runFinSync),
}

// finWithoutStdin адаптирует подкоманду, которая ни о чём не спрашивает.
func finWithoutStdin(f func(args []string, stdout, stderr io.Writer) int,
) func([]string, io.Reader, io.Writer, io.Writer) int {
	return func(a []string, _ io.Reader, o, e io.Writer) int { return f(a, o, e) }
}

// finUsageLine перечисляет подкоманды в устойчивом порядке, чтобы помощь не
// перетасовывалась между прогонами так, как это делает обход карты.
func finUsageLine() string {
	verbs := slices.Sorted(maps.Keys(finCommands))
	return "usage: kbengine fin <" + strings.Join(verbs, "|") + "> [flags]"
}

func runFin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, finUsageLine())
		return 2
	}
	// Как и на верхнем уровне: помощь узнаётся до реестра, иначе просьба о
	// помощи получает ответ «неизвестная подкоманда».
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, finUsageLine())
		return 0
	}
	sub, ok := finCommands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown fin subcommand %q\n", args[0])
		return 2
	}
	// Отказ разбора опознаётся здесь, а не в девяти подкомандах: диспетчер —
	// единственное место, которое знает и о семье, и об исходе.
	watched := &headWriter{w: stderr}
	code := sub(args[1:], stdin, stdout, watched)
	if code != 0 {
		finHint(stderr, watched.head.String(), args[0])
	}
	return code
}

// newULID hands out sortable identifiers. ULID rather than UUIDv4 because the
// ledger is kept in file order: an id that sorts by creation time keeps a
// same-day tie-break meaningful instead of random.
func newULID() string { return ulid.Make().String() }

// ledgerFlags adds the flags every subcommand shares and returns the accessor
// for the ledger path.
func ledgerFlags(fs *flag.FlagSet) *string {
	return fs.String("ledger", "", "path to transactions.jsonl")
}

// filterFlags adds the flags that narrow a ledger.
func filterFlags(fs *flag.FlagSet) (year, month *int, category, account, kind *string) {
	year = fs.Int("year", 0, "restrict to a year")
	month = fs.Int("month", 0, "restrict to a month (1-12)")
	category = fs.String("cat", "", "restrict to a category")
	account = fs.String("account", "", "restrict to an account")
	kind = fs.String("kind", "", "restrict to expense or income")
	return year, month, category, account, kind
}

// runFinImport is the one-time migration out of the spreadsheet.
func runFinImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	from := fs.String("from", "", "path to Учёт_финансов.xlsx")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin import: --ledger is required")
		return 2
	}
	if *from == "" {
		fmt.Fprintln(stderr, "fin import: --from is required")
		return 2
	}
	// Import is a migration, not a sync: it has no way to tell which of the two
	// files is ahead, so it refuses rather than guessing with the whole ledger
	// at stake.
	if _, err := os.Stat(*ledgerPath); err == nil {
		fmt.Fprintf(stderr, "fin import: %s already exists — import is a one-time migration, use fin sync afterwards\n", *ledgerPath)
		return 1
	}

	now := time.Now()
	led, err := financexlsx.Read(*from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin import: %v\n", err)
		return 1
	}
	recs, err := finance.Import(led.Transactions, newULID, now)
	if err != nil {
		fmt.Fprintf(stderr, "fin import: %v\n", err)
		return 1
	}
	finance.Sort(recs)
	if err := financejsonl.Save(*ledgerPath, recs, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin import: %v\n", err)
		return 1
	}

	// These counts are the reconciliation against the previous build. They are
	// printed even on success because the migration is only trustworthy if they
	// match what the spreadsheet reported.
	s := finance.Summarize(recs)
	fmt.Fprintf(stdout, "fin import: %d expense(s) %s, %d income(s) %s, net %s → %s\n",
		s.ExpenseCount, s.Expenses, s.IncomeCount, s.Income, s.Net, *ledgerPath)
	if len(led.Accounts) > 0 {
		fmt.Fprintf(stdout, "fin import: %d account(s), balance %s (from the workbook)\n",
			len(led.Accounts), led.TotalBalance())
	}
	return 0
}

// runFinAdd appends one entry.
func runFinAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	amount := fs.String("amount", "", "amount, e.g. 1500 or 1 500,50")
	kind := fs.String("kind", domain.KindExpense, "expense or income")
	category := fs.String("cat", "", "category (required for an expense)")
	sub := fs.String("sub", "", "subcategory")
	place := fs.String("place", "", "where the money went")
	note := fs.String("note", "", "free-form description")
	source := fs.String("source", "", "how the record was captured, e.g. Чек")
	account := fs.String("account", "", "which account the money moved through")
	date := fs.String("date", "", "date as YYYY-MM-DD (default today)")
	force := fs.Bool("force", false, "write even if the ledger already holds the same entry")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin add: --ledger is required")
		return 2
	}
	if *amount == "" {
		fmt.Fprintln(stderr, "fin add: --amount is required")
		return 2
	}

	// ParseMoney, not MoneyFromFloat: this is text a person typed, so more
	// precision than a kopeck is a typo and gets reported rather than rounded.
	money, err := domain.ParseMoney(*amount)
	if err != nil {
		fmt.Fprintf(stderr, "fin add: %v\n", err)
		return 1
	}
	var when time.Time
	if *date != "" {
		if when, err = time.Parse(time.DateOnly, *date); err != nil {
			fmt.Fprintf(stderr, "fin add: --date %q: expected YYYY-MM-DD\n", *date)
			return 1
		}
	}

	rec, err := appendChecked(*ledgerPath, finance.AddParams{
		Kind:        *kind,
		Date:        when,
		Amount:      money,
		Category:    *category,
		Subcategory: *sub,
		Place:       *place,
		Description: *note,
		Source:      *source,
		Account:     *account,
	}, *force, func(c finance.Correction) {
		// Подстановка называется вслух: человек набрал одно, записано другое,
		// и узнать об этом из отчёта через месяц — то же самое молчание.
		fmt.Fprintf(stdout, "fin add: %s — записано %q, набрано %q (так уже пишут в базе)\n",
			c.Field, c.Used, c.Typed)
	}, func(w string) {
		// В stderr, а не в stdout: это состояние словаря, а не часть отчёта о
		// записи, и конвейеру, читающему вывод команды, оно не нужно.
		fmt.Fprintf(stderr, "fin add: %s\n", w)
	})
	if err != nil {
		fmt.Fprintf(stderr, "fin add: %v\n", err)
		return 1
	}
	tx := rec.Transaction()
	fmt.Fprintf(stdout, "fin add: %s  %s  %10s  %s %s\n",
		tx.ID(), tx.Date().Format(time.DateOnly), tx.Amount(), tx.Category(), tx.Description())
	return 0
}

// appendToLedger records one new entry in the ledger and returns it.
//
// One function for every surface that writes: the command below and the
// terminal's entry form both call it. A second copy of load → add → sort → save
// is how the ledger ends up with rows that only one surface can read — the
// spreadsheet already taught that lesson at the cost of fourteen rows.
// ErrRepeat is returned when the entry repeats one already in the ledger.
//
// A sentinel rather than a plain error so every surface can tell "this is a
// repeat" from "the file would not open" and offer the right thing: confirming
// a repeat is a decision, failing to write is not.
var ErrRepeat = errors.New("такая запись уже есть")

// repeatSubject — чем запись называется в отказе. У расхода это место, у
// дохода места нет вовсе, и тогда говорит источник: строка «200 ₽ ·  · Сбербанк»
// не отвечает на вопрос, о какой записи речь.
//
// Одно место на оба пути отказа: добавление и правка обязаны называть найденную
// запись одинаково, иначе человек решит, что нашлись разные.
func repeatSubject(tx domain.Transaction) string {
	if p := tx.Place(); p != "" {
		return p
	}
	return tx.Source()
}

// appendChecked is the single write path, and the single place the repeat check
// lives.
//
// Deliberately here rather than in each surface: the command, the entry form,
// the one-line entry and anything added later all go through this function, so
// a guard placed here cannot be walked around by using a different screen.
// Placed in any of them, it would be a rule the next surface forgets.
func appendChecked(ledgerPath string, p finance.AddParams, force bool,
	note func(finance.Correction), warn func(string),
) (finance.Record, error) {
	// Замок держится на всём чтении-правке-записи, а не на самой записи.
	// Проверка повтора судит по прочитанному состоянию, и если между чтением и
	// записью успел вклиниться второй процесс, победит последний: замер восемью
	// одновременными `fin add` давал одну строку при восьми успехах.
	var rec finance.Record
	err := filelock.With(ledgerPath, func() error {
		var err error
		rec, err = appendUnderLock(ledgerPath, p, force, note, warn)
		return err
	})
	return rec, err
}

// appendUnderLock — то же самое, но уже под замком. Отдельной функцией, чтобы
// путь записи читался сверху вниз, а не через отступ замыкания.
func appendUnderLock(ledgerPath string, p finance.AddParams, force bool,
	note func(finance.Correction), warn func(string),
) (finance.Record, error) {
	recs, err := financejsonl.Load(ledgerPath, time.Now)
	if err != nil {
		return finance.Record{}, err
	}
	// Написание приводится к тому, что в базе уже есть, до проверки на повтор:
	// иначе «транспорт» и «Транспорт» считались бы разными тратами.
	//
	// Словарь читается рядом с леджером и решает раньше частоты: он хранит
	// решения владельца, а частота — след старых записей. Нет словаря — не
	// беда, тогда решает только частота.
	// Ошибка чтения словаря отбрасывалась целиком, и это скрывало два разных
	// случая: словаря нет (нормально — решает частота) и словарь спорит сам с
	// собой (не нормально: подстановка становилась случайной). Первый молчит
	// по-прежнему, второй называется.
	voc, vocErr := financevocab.Load(financevocab.PathNextTo(ledgerPath))
	if warn != nil && errors.Is(vocErr, finance.ErrVocabularyConflict) {
		warn(vocErr.Error())
	}
	p, fixed := finance.CanonicalWith(recs, voc, p)
	for _, c := range fixed {
		if note != nil {
			note(c)
		}
	}
	// Дубль ищется всегда, а не только без --force: при осознанном повторе
	// найденная запись становится следом решения. Иначе подтверждённый повтор и
	// проскочивший дубль в файле неразличимы, и разбор недельной давности
	// упирается в гадание — ровно та неразрешимость, ради которой заведён гейт.
	if dup := finance.Duplicate(recs, p); dup != nil {
		tx := dup.Transaction()
		if !force {
			return finance.Record{}, fmt.Errorf("%w: %s · %s · %s · %s (%s) — повторить осознанно: --force",
				ErrRepeat, tx.Date().Format(time.DateOnly), tx.Amount(),
				repeatSubject(tx), tx.Account(), tx.ID())
		}
		p.RepeatOf = tx.ID()
	}
	rec, err := finance.Add(p, newULID, time.Now)
	if err != nil {
		return finance.Record{}, err
	}
	recs = append(recs, rec)
	finance.Sort(recs)
	if err := financejsonl.Save(ledgerPath, recs, time.Now); err != nil {
		return finance.Record{}, err
	}
	return rec, nil
}

// runFinList prints the matching rows.
func runFinList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	year, month, category, account, kind := filterFlags(fs)
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin list: --ledger is required")
		return 2
	}

	recs, err := financejsonl.Load(*ledgerPath, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin list: %v\n", err)
		return 1
	}
	matched := finance.Match(recs, finance.Filter{
		Year: *year, Month: time.Month(*month), Category: *category, Account: *account, Kind: *kind,
	})
	for _, r := range matched {
		tx := r.Transaction()
		// Подтверждённый повтор называется прямо в строке: иначе две одинаковые
		// траты рядом снова читаются как вопрос без ответа.
		var repeat string
		if id := tx.RepeatOf(); id != "" {
			repeat = fmt.Sprintf("  ↺ повтор подтверждён поверх %s", id)
		}
		fmt.Fprintf(stdout, "%s  %s  %12s  %-14s %s%s\n",
			tx.Date().Format(time.DateOnly), tx.ID(), tx.Amount(), tx.Category(), tx.Description(), repeat)
	}
	fmt.Fprintf(stdout, "fin list: %d of %d record(s)\n", len(matched), len(recs))
	return 0
}

// runFinReport prints the totals.
func runFinReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	year, month, category, account, kind := filterFlags(fs)
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin report: --ledger is required")
		return 2
	}

	recs, err := financejsonl.Load(*ledgerPath, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin report: %v\n", err)
		return 1
	}
	s := finance.Summarize(finance.Match(recs, finance.Filter{
		Year: *year, Month: time.Month(*month), Category: *category, Account: *account, Kind: *kind,
	}))

	fmt.Fprintf(stdout, "expenses  %14s  (%d)\n", s.Expenses, s.ExpenseCount)
	fmt.Fprintf(stdout, "income    %14s  (%d)\n", s.Income, s.IncomeCount)
	fmt.Fprintf(stdout, "net       %14s\n", s.Net)
	// Исключённое называется рядом с итогом, а не подразумевается: сумма строк
	// в журнале не сходится с расходами ровно на эту величину, и без строки
	// расхождение приходится объяснять себе самому. Молчит, когда исключать
	// было нечего.
	if s.ExcludedTransferCount > 0 {
		fmt.Fprintf(stdout, "переводы себе — не в итогах: %s (%d)\n",
			s.ExcludedTransfers, s.ExcludedTransferCount)
	}
	writeBreakdown(stdout, "по категориям", s.ByCategory)
	writeBreakdown(stdout, "по счетам", s.ByAccount)
	return 0
}

// writeBreakdown prints one titled block. The title is not decoration: the two
// blocks are the same shape, and the ledger has a category called «Банк» that
// otherwise sits four lines above a list of banks with nothing to tell them
// apart.
func writeBreakdown(stdout io.Writer, title string, rows []finance.CategoryTotal) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(stdout, "\n  %s\n", title)
	for _, c := range rows {
		fmt.Fprintf(stdout, "  %-22s %14s  (%d)\n", c.Category, c.Total, c.Count)
	}
}
