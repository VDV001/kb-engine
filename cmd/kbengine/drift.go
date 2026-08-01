package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/linkcheck"
	"github.com/daniil/kb-engine/internal/usecase/drift"
)

func runDrift(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	delay := fs.Duration("delay", 500*time.Millisecond, "pause between requests")
	limit := fs.Int("limit", 0, "check at most N urls (0 = all)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "drift: --catalog is required")
		return 2
	}

	svc := drift.NewService(catalogjson.FileLoader{Path: *catalogPath}, linkcheck.New(*timeout, *delay))
	svc.Limit = *limit
	rep, err := svc.Scan(time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "drift: %v\n", err)
		return 1
	}
	printDriftReport(stdout, rep)
	return 0
}

// printDriftReport prints what was established and — first — what was not.
// The order is the point: a reader who stops after the first lines must leave
// knowing the limits of the scan, not just its findings.
func printDriftReport(stdout io.Writer, rep drift.Report) {
	answered := len(rep.Results)
	fmt.Fprintf(stdout, "drift: записей в каталоге %d\n", rep.TotalEntries)
	fmt.Fprintf(stdout, "  НЕ проверено: %d без ссылки, %d не ответили по сети, %d не дошла очередь (--limit)\n",
		rep.WithoutURL, len(rep.Unreachable), rep.NotAttempted)
	fmt.Fprintf(stdout, "  без вердикта: %d ответов (403/429/5xx — говорят о сервере, не о статье; нужен браузер)\n",
		rep.Undecidable())
	fmt.Fprintf(stdout, "  проверено с вердиктом: %d из %d\n",
		answered-rep.Undecidable(), rep.TotalEntries)

	gone := rep.Actionable()
	if len(gone) == 0 {
		fmt.Fprintln(stdout, "  мёртвых ссылок не найдено")
		return
	}
	fmt.Fprintf(stdout, "\nмёртвые ссылки (%d):\n", len(gone))
	for _, r := range gone {
		fmt.Fprintf(stdout, "  id=%d code=%d %s\n      %s\n", r.EntryID, r.Code, r.URL, r.Title)
	}
}
