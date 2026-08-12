package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
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
	apply := fs.Bool("apply", false, "record the results in the catalog (drift_check_date / drift_http_code)")
	updateURLs := fs.Bool("update-urls", false, "also replace an entry's url with the address its redirect points at (needs --apply)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "drift: --catalog is required")
		return 2
	}
	// Выполнимость намерения выясняется до работы, а не после неё. Скан —
	// самое дорогое, что делает движок: на живом каталоге это 1342 запроса по
	// сети, и все они уходили впустую, потому что об ошибке в командной строке
	// сообщалось последней строкой.
	if *updateURLs && !*apply {
		fmt.Fprintln(stderr, "drift: --update-urls меняет адреса записей и требует --apply")
		return 2
	}

	// Ctrl-C прекращает опрос, но не выбрасывает работу: прогон по живой базе
	// идёт минутами, и полученные ответы записываются, если попросили --apply.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc := drift.NewService(catalogjson.FileLoader{Path: *catalogPath}, linkcheck.New(*timeout, *delay))
	svc.Limit = *limit
	rep, err := svc.Scan(ctx, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "drift: %v\n", err)
		return 1
	}
	if rep.Stopped {
		fmt.Fprintln(stdout, "drift: скан прерван — ниже только то, что успели спросить")
	}
	printDriftReport(stdout, rep)
	printMoved(stdout, rep, *updateURLs, *apply)

	if !*apply {
		if len(rep.Results) > 0 {
			fmt.Fprintln(stdout, "\n(результат нигде не сохранён — добавьте --apply, чтобы база запомнила проверку)")
		}
		return 0
	}
	records := make([]catalogjson.DriftRecord, 0, len(rep.Results))
	for _, r := range rep.Results {
		records = append(records, catalogjson.DriftRecord{
			EntryID: r.EntryID, CheckedAt: r.CheckedAt, Code: r.Code, NewURL: r.Location,
		})
	}
	write := catalogjson.ApplyDrift
	if *updateURLs {
		write = catalogjson.ApplyDriftWithURLs
	}
	n, err := write(*catalogPath, records)
	if err != nil {
		fmt.Fprintf(stderr, "drift: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nзаписано в каталог: %d запис(ей)\n", n)
	return 0
}

// printMoved lists the addresses that redirect elsewhere. It prints whether or
// not they will be written: the owner has to see what an address change would
// do before asking for it, and has to know the addresses are stale even if he
// never asks.
func printMoved(stdout io.Writer, rep drift.Report, updating, applying bool) {
	moved := rep.Moved()
	if len(moved) == 0 {
		return
	}
	verb := "устарели (адрес в базе не канонический)"
	if updating && applying {
		verb = "будут обновлены"
	}
	fmt.Fprintf(stdout, "\nадреса, отвечающие редиректом — %d %s:\n", len(moved), verb)
	for _, r := range moved {
		fmt.Fprintf(stdout, "  id=%d %s\n      → %s\n", r.EntryID, r.URL, r.Location)
	}
	if !updating {
		fmt.Fprintln(stdout, "  (обновить: --update-urls вместе с --apply)")
	}
}

// printDriftReport prints what was established and — first — what was not.
// The order is the point: a reader who stops after the first lines must leave
// knowing the limits of the scan, not just its findings.
func printDriftReport(stdout io.Writer, rep drift.Report) {
	answered := len(rep.Results)
	fmt.Fprintf(stdout, "drift: записей в каталоге %d\n", rep.TotalEntries)
	// Причина, по которой до адреса не дошла очередь, у выборки и у прерванного
	// прогона разная, и путать их нельзя: выборку человек выбрал сам, а
	// остановленный скан — незаконченная работа.
	queueReason := "--limit"
	if rep.Stopped {
		queueReason = "скан прерван"
	}
	fmt.Fprintf(stdout, "  НЕ проверено: %d без ссылки, %d не ответили по сети или ответили непонятным кодом, %d не дошла очередь (%s)\n",
		rep.WithoutURL, len(rep.Unreachable), rep.NotAttempted, queueReason)
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
