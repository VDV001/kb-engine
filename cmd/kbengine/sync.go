package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// syncStateName is the baseline file, kept next to the ledger it describes.
const syncStateName = ".sync-state.json"

// runFinSync keeps the workbook and the ledger in step.
func runFinSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	from := fs.String("from", "", "path to Учёт_финансов.xlsx")
	initialize := fs.Bool("init", false, "give every row a stable id on both sides")
	migrateIDs := fs.Bool("migrate-ids", false,
		"move ids off the column the account uses, on a book paired by an older version")
	backfillIDs := fs.Bool("backfill-ids", false,
		"store the id of every row the workbook identifies only by position")
	resolve := fs.String("resolve", "", "on a conflict, take one side: jsonl or xlsx")
	dryRun := fs.Bool("dry-run", false, "report what would happen and change nothing")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *from == "" {
		fmt.Fprintln(stderr, "fin sync: --from is required")
		return 2
	}
	// Ahead of the --ledger check: this repairs the workbook's own layout and
	// has no pairing to keep in step. Demanding the ledger here would send
	// someone who is holding a refusal looking for a second path they do not
	// need yet.
	if *migrateIDs {
		return migrateWorkbookIDs(*from, stdout, stderr)
	}
	// Alongside --migrate-ids, and for the same reason: this repairs the
	// workbook's own identity column and keeps no pairing in step.
	if *backfillIDs {
		return backfillWorkbookIDs(*from, stdout, stderr)
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin sync: --ledger is required")
		return 2
	}
	if *initialize {
		return pairWorkbookWithLedger(*from, *ledgerPath, *dryRun, stdout, stderr)
	}
	forced, ok := forcedDirection(*resolve)
	if !ok {
		fmt.Fprintf(stderr, "fin sync: --resolve %q: expected jsonl or xlsx\n", *resolve)
		return 2
	}
	return syncWorkbookAndLedger(*from, *ledgerPath, forced, *dryRun, stdout, stderr)
}

// backfillWorkbookIDs stores the id of every row the workbook identifies only by
// position, so a row inserted above stops moving anyone's identity.
//
// A book where every row already carries an id is a normal outcome and is said
// out loud: a command that prints nothing is indistinguishable from one that did
// not run.
func backfillWorkbookIDs(from string, stdout, stderr io.Writer) int {
	stored, err := financexlsx.BackfillIDs(from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync --backfill-ids: %v\n", err)
		return 1
	}
	if stored == 0 {
		fmt.Fprintln(stdout, "fin sync --backfill-ids: nothing to store — every row already carries an id")
		return 0
	}
	fmt.Fprintf(stdout, "fin sync --backfill-ids: %d id(s) stored → %s\n", stored, from)
	return 0
}

// migrateWorkbookIDs moves the ids off the column the account uses, on a book
// paired before that placement rule existed.
//
// Every write into such a book is refused, so this is the only way out of that
// state and reports plainly which of the two things happened — a book that
// needed nothing is a normal outcome, not a failure.
func migrateWorkbookIDs(from string, stdout, stderr io.Writer) int {
	m, err := financexlsx.MigrateIDColumn(from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync --migrate-ids: %v\n", err)
		return 1
	}
	switch {
	case !m.Rewrote:
		fmt.Fprintf(stdout, "fin sync --migrate-ids: nothing to move — ids are already in column %s\n", m.Column)
	case m.Moved == 0:
		// The file was rewritten even though no row carried an id, and saying
		// "nothing to move" about a book that now has a backup behind it is the kind
		// of report that makes the backup surprising.
		fmt.Fprintf(stdout, "fin sync --migrate-ids: no ids to move — the header moved to column %s → %s\n", m.Column, from)
	default:
		fmt.Fprintf(stdout, "fin sync --migrate-ids: %d id(s) moved to column %s → %s\n", m.Moved, m.Column, from)
	}
	return 0
}

// forcedDirection turns --resolve into a direction. An empty flag means "follow
// the diff", which is the normal case.
func forcedDirection(resolve string) (finance.Direction, bool) {
	switch resolve {
	case "":
		return finance.DirectionNone, true
	case "jsonl":
		return finance.DirectionToWorkbook, true
	case "xlsx":
		return finance.DirectionToLedger, true
	default:
		return finance.DirectionNone, false
	}
}

// pairWorkbookWithLedger gives every row a stable id on both sides and writes
// the ledger and the baseline from the same read.
func pairWorkbookWithLedger(from, ledgerPath string, dryRun bool, stdout, stderr io.Writer) int {
	// Замок держится на всей операции: синк читает обе стороны и перезаписывает
	// журнал целиком, поэтому `fin add`, вклинившийся между чтением и записью,
	// пропал бы без следа. Ждать здесь правильно — вторая команда допишется
	// после, а не поверх.
	code := 1
	if err := filelock.With(ledgerPath, func() error {
		code = pairUnderLock(from, ledgerPath, dryRun, stdout, stderr)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}
	return code
}

func pairUnderLock(from, ledgerPath string, dryRun bool, stdout, stderr io.Writer) int {
	// An existing ledger may hold entries made with fin add that the workbook has
	// never seen. Re-pairing would drop them, so the decision stays with a person.
	if _, err := os.Stat(ledgerPath); err == nil {
		fmt.Fprintf(stderr, "fin sync --init: %s already exists — remove it first if you mean to re-pair from the workbook\n", ledgerPath)
		return 1
	}

	// Asked before the read, and before the dry-run branch: a dry run answers
	// "what would happen", and what would happen to a workbook someone has open
	// is a refusal. Read does not check the lock — only the writers do — so
	// without this a dry run reports rows it could never have paired.
	if err := financexlsx.CheckLock(from); err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}

	now := time.Now()
	led, err := financexlsx.Read(from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}

	// Rows that already carry an id keep it. Minting a new one would orphan every
	// reference to the old one, including a ledger someone still has a copy of.
	assign := make(map[string]string)
	ids := make([]string, 0, len(led.Transactions))
	for _, tx := range led.Transactions {
		id := tx.ID()
		if financexlsx.IsPositionalID(id) {
			id = newULID()
			assign[tx.ID()] = id
		}
		ids = append(ids, id)
	}

	// The ids are already decided, so the generator hands out the prepared ones in
	// order — Import still does the duplicate check and the record building.
	//
	// Import runs before anything is written so that a dry run reports the same
	// count the real run would, and so that a workbook that fails the check keeps
	// its rows unmarked.
	next := 0
	recs, err := finance.Import(led.Transactions, func() string {
		id := ids[next]
		next++
		return id
	}, now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}
	finance.Sort(recs)

	if dryRun {
		fmt.Fprintf(stdout, "fin sync --init (dry run): %d row(s) would be paired, %d newly identified — nothing written\n",
			len(recs), len(assign))
		return 0
	}

	if len(assign) > 0 {
		if err := financexlsx.AssignIDs(from, assign, time.Now); err != nil {
			fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
			return 1
		}
	}
	if err := financejsonl.Save(ledgerPath, recs, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}
	if err := financejsonl.SaveState(statePath(ledgerPath), finance.BaselineOf(recs, now)); err != nil {
		fmt.Fprintf(stderr, "fin sync --init: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "fin sync --init: %d row(s) paired, %d newly identified → %s\n",
		len(recs), len(assign), ledgerPath)
	return 0
}

func statePath(ledgerPath string) string {
	return filepath.Join(filepath.Dir(ledgerPath), syncStateName)
}

// syncInputs is everything a sync reads before it decides anything: both sides
// and the baseline they last agreed on.
type syncInputs struct {
	state    finance.SyncState
	records  []finance.Record
	workbook financexlsx.Ledger
}

// loadSyncInputs reads all three or none. Kept apart from the decision it feeds
// so the decision stays readable — the three identical failure branches were
// most of what made it hard to follow.
func loadSyncInputs(from, ledgerPath string) (syncInputs, error) {
	st, err := financejsonl.LoadState(statePath(ledgerPath))
	if err != nil {
		return syncInputs{}, err
	}
	recs, err := financejsonl.Load(ledgerPath, time.Now)
	if err != nil {
		return syncInputs{}, err
	}
	led, err := financexlsx.Read(from, time.Now)
	if err != nil {
		return syncInputs{}, err
	}
	return syncInputs{state: st, records: recs, workbook: led}, nil
}

// syncWorkbookAndLedger moves data one way, or refuses to move any.
func syncWorkbookAndLedger(from, ledgerPath string, forced finance.Direction, dryRun bool, stdout, stderr io.Writer) int {
	code := 1
	if err := filelock.With(ledgerPath, func() error {
		code = syncUnderLock(from, ledgerPath, forced, dryRun, stdout, stderr)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	return code
}

func syncUnderLock(from, ledgerPath string, forced finance.Direction, dryRun bool, stdout, stderr io.Writer) int {
	in, err := loadSyncInputs(from, ledgerPath)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	st, recs, led := in.state, in.records, in.workbook

	plan := finance.Diff(recs, led.Transactions, st)
	direction := plan.Direction
	if forced != finance.DirectionNone {
		direction = forced
	}

	// Asked once, for the dry run and the real one alike. --init has asked it
	// since it was written; this path had not, so a dry run announced a push
	// into a book an editor was holding and the command that followed refused
	// it. Only a direction that writes the workbook needs the answer — refusing
	// to read while the book is open would make the lock the larger problem.
	if direction == finance.DirectionToWorkbook {
		if err := financexlsx.CheckLock(from); err != nil {
			fmt.Fprintf(stderr, "fin sync: %v\n", err)
			return 1
		}
	}

	if dryRun {
		fmt.Fprintf(stdout, "fin sync (dry run): %s\n", direction)
		writeSummary(stdout, plan)
		writeDiff(stdout, plan, describeRecords(recs), describeTransactions(led.Transactions))
		return 0
	}

	now := time.Now()
	switch direction {
	case finance.DirectionConflict:
		return reportConflict(ledgerPath, plan, describeRecords(recs), describeTransactions(led.Transactions),
			now, stdout, stderr)

	case finance.DirectionNone:
		// A first sync after --init predates the baseline in older setups; record
		// it now that both sides are known to agree.
		if len(st.Rows) == 0 {
			if err := financejsonl.SaveState(statePath(ledgerPath), finance.BaselineOf(recs, now)); err != nil {
				fmt.Fprintf(stderr, "fin sync: %v\n", err)
				return 1
			}
		}
		fmt.Fprintln(stdout, "fin sync: nothing to do — both sides agree")
		return 0

	case finance.DirectionToWorkbook:
		return pushToWorkbook(from, ledgerPath, recs, led.Transactions, now, stdout, stderr)

	case finance.DirectionToLedger:
		return pullFromWorkbook(ledgerPath, recs, led.Transactions, now, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "fin sync: unhandled direction %v\n", direction)
		return 1
	}
}

// reportConflict writes the divergence down and exits non-zero without touching
// either file.
//
// The engine can lay out what differs; it cannot decide which version is the
// one you meant. A merge here would eventually lose a transaction quietly,
// which is worse than any amount of stopping.
func reportConflict(ledgerPath string, plan finance.Plan, inLedger, inWorkbook map[string]string,
	now time.Time, stdout, stderr io.Writer,
) int {
	name := fmt.Sprintf(".conflict-%s.md", now.UTC().Format("2006-01-02T15-04-05Z"))
	path := filepath.Join(filepath.Dir(ledgerPath), name)

	var b strings.Builder
	fmt.Fprintf(&b, "# Sync conflict — %s\n\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "%s\n\nNothing was written. Both files are exactly as they were.\n\n", plan.Reason)
	writeSide(&b, "Ledger", plan.Ledger, inLedger, inWorkbook)
	writeSide(&b, "Workbook", plan.Workbook, inWorkbook, inLedger)
	b.WriteString("## Resolving\n\n" +
		"Pick a side explicitly, having looked at what each one costs:\n\n" +
		"    kbengine fin sync --resolve jsonl   # the ledger wins\n" +
		"    kbengine fin sync --resolve xlsx    # the workbook wins\n\n" +
		"Whichever side loses, its changes above are gone. Backups of the workbook are in .backup/.\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		fmt.Fprintf(stderr, "fin sync: conflict, and the report could not be written: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "fin sync: conflict — %s\n", plan.Reason)
	writeSummary(stdout, plan)
	fmt.Fprintf(stdout, "\nNothing was written. Details: %s\n", path)
	fmt.Fprintln(stdout, "Resolve with --resolve jsonl or --resolve xlsx.")
	return 1
}

// describeRecords and describeTransactions render each row as a line a person
// can weigh: an identifier alone cannot answer which version of the money is
// the one that was meant.
func describeRecords(recs []finance.Record) map[string]string {
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.Transaction().ID()] = describeTx(r.Transaction())
	}
	return out
}

func describeTransactions(txs []domain.Transaction) map[string]string {
	out := make(map[string]string, len(txs))
	for _, tx := range txs {
		out[tx.ID()] = describeTx(tx)
	}
	return out
}

func describeTx(tx domain.Transaction) string {
	parts := []string{tx.Date().Format(time.DateOnly), tx.Amount().String()}
	if c := tx.Category(); c != "" {
		parts = append(parts, c)
	}
	if d := tx.Description(); d != "" {
		parts = append(parts, d)
	}
	if s := tx.Source(); s != "" && !tx.IsExpense() {
		parts = append(parts, s)
	}
	// Счёт расхода печатается наравне с остальным, а его отсутствие называется
	// вслух. Раньше строка без счёта выглядела полной, и трата уехала в книгу
	// ничьей — в разбивке по счетам её нет, а по отчёту не видно, почему.
	if tx.IsExpense() {
		if a := tx.Account(); a != "" {
			parts = append(parts, a)
		} else {
			parts = append(parts, "без счёта")
		}
	}
	return strings.Join(parts, "  ")
}

// writeSide lists one side's changes. Rows it no longer holds are described
// from the other side, which is the only place they still exist.
func writeSide(b *strings.Builder, title string, s finance.Side, primary, fallback map[string]string) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if !s.Moved() {
		b.WriteString("unchanged\n\n")
		return
	}
	for _, group := range []struct {
		label string
		ids   []string
	}{
		{"added", s.Added}, {"modified", s.Modified}, {"removed", s.Removed},
	} {
		if len(group.ids) == 0 {
			continue
		}
		fmt.Fprintf(b, "%s (%d):\n\n", group.label, len(group.ids))
		for _, id := range group.ids {
			what := primary[id]
			if what == "" {
				what = fallback[id]
			}
			if what == "" {
				what = "(no longer on either side)"
			}
			fmt.Fprintf(b, "- `%s` — %s\n", id, what)
		}
		b.WriteString("\n")
	}
}

// writeDiff печатает, какие именно строки тронутся: `+` на появляющихся, `-` на
// уходящих, и обе подряд там, где запись изменилась. Счётчики выше отвечают на
// «сколько», это — на «что именно», и решение принимается по второму.
//
// Цвет добавляется только для терминала. В файле или пайпе ESC-последовательности
// были бы мусором, а diff читают и глазами, и `grep`.
func writeDiff(w io.Writer, plan finance.Plan, ledgerSide, workbookSide map[string]string) {
	color := isTerminal(w)
	for _, side := range []struct {
		label string
		s     finance.Side
		// primary — как запись выглядит на этой стороне, fallback — на другой;
		// удалённую строку описать можно только оттуда, где она ещё есть.
		primary, fallback map[string]string
	}{
		{"ledger", plan.Ledger, ledgerSide, workbookSide},
		{"workbook", plan.Workbook, workbookSide, ledgerSide},
	} {
		if !side.s.Moved() {
			continue
		}
		fmt.Fprintf(w, "\n  %s:\n", side.label)
		for _, id := range side.s.Added {
			writeDiffLine(w, color, '+', describeOf(id, side.primary, side.fallback))
		}
		for _, id := range side.s.Modified {
			// Две строки подряд: в книге записано одно, в леджере другое.
			writeDiffLine(w, color, '-', describeOf(id, side.fallback, side.primary))
			writeDiffLine(w, color, '+', describeOf(id, side.primary, side.fallback))
		}
		for _, id := range side.s.Removed {
			writeDiffLine(w, color, '-', describeOf(id, side.fallback, side.primary))
		}
	}
}

func describeOf(id string, primary, fallback map[string]string) string {
	if s := primary[id]; s != "" {
		return s
	}
	if s := fallback[id]; s != "" {
		return s
	}
	return id
}

func writeDiffLine(w io.Writer, color bool, sign byte, text string) {
	line := fmt.Sprintf("  %c %s", sign, text)
	if !color {
		fmt.Fprintln(w, line)
		return
	}
	const (
		green = "\x1b[32m"
		red   = "\x1b[31m"
		reset = "\x1b[0m"
	)
	tint := green
	if sign == '-' {
		tint = red
	}
	fmt.Fprintln(w, tint+line+reset)
}

// isTerminal отвечает, смотрит ли человек в терминал прямо сейчас. Проверка идёт
// по типу назначения, а не по имени команды: тот же вывод уходит и в файл.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func writeSummary(w io.Writer, plan finance.Plan) {
	for _, side := range []struct {
		label string
		s     finance.Side
	}{
		{"ledger", plan.Ledger}, {"workbook", plan.Workbook},
	} {
		if !side.s.Moved() {
			continue
		}
		fmt.Fprintf(w, "  %-9s +%d added, %d modified, -%d removed\n",
			side.label, len(side.s.Added), len(side.s.Modified), len(side.s.Removed))
	}
}

// pushToWorkbook makes the workbook match the ledger.
func pushToWorkbook(from, ledgerPath string, recs []finance.Record, workbook []domain.Transaction,
	now time.Time, stdout, stderr io.Writer,
) int {
	upserts, removals := finance.ToWorkbook(recs, workbook)
	if err := financexlsx.ApplyRows(from, upserts, removals, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	if err := financejsonl.SaveState(statePath(ledgerPath), finance.BaselineOf(recs, now)); err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "fin sync: %s — %d row(s) written, %d cleared\n",
		finance.DirectionToWorkbook, len(upserts), len(removals))
	warnUnderstated(from, recs, stdout)
	return 0
}

// warnUnderstated называет траты, которые вычтутся из остатка, уже включавшего
// их.
//
// Такая трата датирована днём подтверждения баланса и записана после его
// момента: движок предполагает «человек её ещё не видел», и для записи задним
// числом это предположение неверно. Арифметику при этом менять нельзя — расчёт
// обещает занижать и никогда не завышать, — поэтому здесь говорится вслух то,
// что иначе человек находит сам, сверяя экран с приложением банка.
//
// Молчит, когда сказать нечего: предупреждение, приходящее всегда, перестают
// читать. Молчит и когда книгу или состояние прочитать не удалось — синхронизация
// уже прошла, и ронять её отчёт из-за подсказки было бы хуже неё самой.
func warnUnderstated(from string, recs []finance.Record, stdout io.Writer) {
	led, err := financexlsx.Read(from, time.Now)
	if err != nil {
		return
	}
	confs, err := balancestate.Load(balancestate.PathNextTo(from))
	if err != nil || len(confs) == 0 {
		return
	}

	type hit struct {
		tx   domain.Transaction
		bank string
	}
	var hits []hit
	for _, r := range recs {
		tx := r.Transaction()
		for _, acc := range led.Accounts {
			if finance.MayUnderstate(tx, acc, confs) {
				hits = append(hits, hit{tx, acc.Bank()})
			}
		}
	}
	if len(hits) == 0 {
		return
	}

	fmt.Fprintf(stdout, "\n  записано после того, как баланс подтвердили, и датировано тем же днём:\n")
	for _, h := range hits {
		fmt.Fprintf(stdout, "    %s  %s  %s\n", h.tx.Amount(), h.bank, h.tx.Place())
	}
	fmt.Fprintf(stdout, "  если эти траты уже прошли по банку к моменту подтверждения — расчётный\n")
	fmt.Fprintf(stdout, "  остаток занижен на них; подтвердите баланс заново\n")
}

// pullFromWorkbook makes the ledger match the workbook.
func pullFromWorkbook(ledgerPath string, recs []finance.Record, workbook []domain.Transaction,
	now time.Time, stdout, stderr io.Writer,
) int {
	// Rows repeating what the ledger already holds are refused before anything is
	// written. Accepting them quietly is how one expense becomes two: a row put
	// into the cells past the engine carries no id, so the sync cannot tell it is
	// the same purchase and adopts it as new.
	//
	// Refused rather than skipped — skipping would drop a row that might be
	// genuine — and named on the way out, because the fix is in the workbook and
	// the person is the only one who can make it.
	if repeats := finance.RepeatsFromWorkbook(recs, workbook); len(repeats) > 0 {
		fmt.Fprintf(stderr, "fin sync: в книге повторов уже записанного: %d\n", len(repeats))
		for _, r := range repeats {
			row, was := r.Row, r.Existing.Transaction()
			what := row.Place()
			if what == "" {
				what = row.Source()
			}
			fmt.Fprintf(stderr, "  %s · %s · %s · %s — уже записано как %s\n",
				row.Date().Format(time.DateOnly), row.Amount(), what, row.Account(), was.ID())
		}
		fmt.Fprintln(stderr, "  удалите лишнюю строку из книги, если это повтор")
		return 1
	}

	out, err := finance.ApplyToLedger(recs, workbook, now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	if err := financejsonl.Save(ledgerPath, out, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	if err := financejsonl.SaveState(statePath(ledgerPath), finance.BaselineOf(out, now)); err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "fin sync: %s — ledger now holds %d row(s)\n",
		finance.DirectionToLedger, len(out))
	return 0
}
