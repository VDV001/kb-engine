package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	resolve := fs.String("resolve", "", "on a conflict, take one side: jsonl or xlsx")
	dryRun := fs.Bool("dry-run", false, "report what would happen and change nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin sync: --ledger is required")
		return 2
	}
	if *from == "" {
		fmt.Fprintln(stderr, "fin sync: --from is required")
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
	// An existing ledger may hold entries made with fin add that the workbook has
	// never seen. Re-pairing would drop them, so the decision stays with a person.
	if _, err := os.Stat(ledgerPath); err == nil {
		fmt.Fprintf(stderr, "fin sync --init: %s already exists — remove it first if you mean to re-pair from the workbook\n", ledgerPath)
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
	if err := financejsonl.Save(ledgerPath, recs); err != nil {
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

// syncWorkbookAndLedger moves data one way, or refuses to move any.
func syncWorkbookAndLedger(from, ledgerPath string, forced finance.Direction, dryRun bool, stdout, stderr io.Writer) int {
	st, err := financejsonl.LoadState(statePath(ledgerPath))
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	recs, err := financejsonl.Load(ledgerPath, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	led, err := financexlsx.Read(from, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}

	plan := finance.Diff(recs, led.Transactions, st)
	direction := plan.Direction
	if forced != finance.DirectionNone {
		direction = forced
	}

	if dryRun {
		fmt.Fprintf(stdout, "fin sync (dry run): %s\n", direction)
		writeSummary(stdout, plan)
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
	return 0
}

// pullFromWorkbook makes the ledger match the workbook.
func pullFromWorkbook(ledgerPath string, recs []finance.Record, workbook []domain.Transaction,
	now time.Time, stdout, stderr io.Writer,
) int {
	out, err := finance.ApplyToLedger(recs, workbook, now)
	if err != nil {
		fmt.Fprintf(stderr, "fin sync: %v\n", err)
		return 1
	}
	if err := financejsonl.Save(ledgerPath, out); err != nil {
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
