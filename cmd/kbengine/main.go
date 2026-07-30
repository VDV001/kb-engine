// Command kbengine is the CLI entry point for the KB engine.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	root "github.com/daniil/kb-engine"
	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// version is the build version. It defaults to "dev" and is overridden at
// release time via -ldflags "-X main.version=<tag>" (see .goreleaser.yaml).
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns the process exit code. It takes its
// I/O as parameters so it is testable without touching os globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kbengine <command> [flags]\ncommands: audit, audit-tasks, changelog, dedup, fin, inbox, serve, version")
		return 2
	}
	switch args[0] {
	case "version":
		return runVersion(stdout)
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "audit-tasks":
		return runAuditTasks(args[1:], os.Stdin, stdout, stderr)
	case "changelog":
		return runChangelog(args[1:], stdout, stderr)
	case "dedup":
		return runDedup(args[1:], stdout, stderr)
	case "fin":
		return runFin(args[1:], stdout, stderr)
	case "inbox":
		return runInbox(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

// runVersion prints the build version, plus VCS revision and build time when
// the binary carries Go module build info (e.g. installed via `go install`).
func runVersion(stdout io.Writer) int {
	v := version
	var commit, date string
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				date = s.Value
			}
		}
	}
	fmt.Fprintf(stdout, "kbengine %s\n", v)
	if commit != "" {
		fmt.Fprintf(stdout, "commit: %s\n", commit)
	}
	if date != "" {
		fmt.Fprintf(stdout, "built:  %s\n", date)
	}
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	configPath := fs.String("analytics-config", "", "optional path to analytics_config.json (semantic layer)")
	ledgerPath := fs.String("ledger", "", "optional path to transactions.jsonl (enables the finances view)")
	workbookPath := fs.String("from", "", "optional path to Учёт_финансов.xlsx (account balances)")
	changelogPath := fs.String("changelog", "", "optional path to CHANGELOG.md («Что нового» in Settings)")
	// Loopback by default. With --ledger this process serves four years of
	// personal transactions with places, notes and balances; ":8080" would hand
	// them to anyone on the network. Binding wider stays possible, but as a
	// choice someone makes rather than one they inherit.
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "serve: --catalog is required")
		return 2
	}

	handler, err := buildServeHandler(*catalogPath, *configPath, *ledgerPath, *workbookPath, *changelogPath)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(stdout, "kbengine: serving dashboard on %s (catalog %s)\n", *addr, *catalogPath)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// ledgerFinances reads the two sources the Finances view needs. The rows live
// in the ledger; the account balances exist only in the workbook, so a
// deployment can have rows and no balances (workbook path empty) but not the
// other way round.
//
// Both files are read per request rather than cached: they are edited by hand
// while the dashboard is open, and a stale balance is worse than a re-read.
type ledgerFinances struct{ ledgerPath, workbookPath string }

func (f ledgerFinances) Finances() (httpapi.Finances, error) {
	recs, err := financejsonl.Load(f.ledgerPath, time.Now)
	if err != nil {
		return httpapi.Finances{}, err
	}
	out := httpapi.Finances{Transactions: make([]domain.Transaction, 0, len(recs))}
	for _, r := range recs {
		out.Transactions = append(out.Transactions, r.Transaction())
	}
	if f.workbookPath != "" {
		led, err := financexlsx.Read(f.workbookPath, time.Now)
		if err != nil {
			return httpapi.Finances{}, err
		}
		out.Accounts = led.Accounts
	}
	return out, nil
}

func buildServeHandler(catalogPath, configPath, ledgerPath, workbookPath, changelogPath string) (http.Handler, error) {
	loader := catalogjson.FileLoader{Path: catalogPath}
	front, err := root.Frontend()
	if err != nil {
		return nil, err
	}
	// Перечитывается на каждый запрос — как и каталог. Отсутствие пути — не
	// ошибка, а пустой семантический слой; падение при старте проверяет, что
	// файл хотя бы читается сейчас.
	cfg := func() (analyticsconfig.Config, error) { return analyticsconfig.Config{}, nil }
	if configPath != "" {
		if _, err := analyticsconfig.Load(configPath); err != nil {
			return nil, err
		}
		cfg = func() (analyticsconfig.Config, error) { return analyticsconfig.Load(configPath) }
	}
	// Nil, not an empty struct: the handler distinguishes "no ledger configured"
	// from "ledger configured and unreadable", and only the second is an error.
	var fin httpapi.Financier
	if ledgerPath != "" {
		fin = ledgerFinances{ledgerPath: ledgerPath, workbookPath: workbookPath}
	}
	var chlog httpapi.ChangelogLoader
	if changelogPath != "" {
		if _, err := os.ReadFile(changelogPath); err != nil {
			return nil, fmt.Errorf("changelog: %w", err)
		}
		chlog = func() (changelog.Document, error) {
			raw, err := os.ReadFile(changelogPath)
			if err != nil {
				return changelog.Document{}, err
			}
			return changelog.Parse(string(raw)), nil
		}
	}
	return httpapi.NewServer(query.NewService(loader), audit.NewService(loader),
		analytics.NewService(loader), fin, cfg, chlog, front), nil
}

func runDedup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dedup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "dedup: --catalog is required")
		return 2
	}

	svc := audit.NewService(catalogjson.FileLoader{Path: *catalogPath})
	groups, err := svc.Duplicates()
	if err != nil {
		fmt.Fprintf(stderr, "dedup: %v\n", err)
		return 1
	}
	for _, g := range groups {
		fmt.Fprintf(stdout, "[%s] ids=%v key=%q\n", g.Kind, g.EntryIDs, g.Key)
	}
	fmt.Fprintf(stdout, "%d duplicate group(s)\n", len(groups))
	return 0
}

func runAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	check := fs.String("check", "all", "which audit to run: outdated|canonical|canonical-health|supersession|age|all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "audit: --catalog is required")
		return 2
	}

	svc := audit.NewService(catalogjson.FileLoader{Path: *catalogPath})
	selected, ok := selectAudits(*check, svc, time.Now())
	if !ok {
		fmt.Fprintf(stderr, "audit: unknown --check %q (want outdated|canonical|canonical-health|supersession|age|all)\n", *check)
		return 2
	}

	total := 0
	for _, a := range selected {
		findings, err := a.run()
		if err != nil {
			fmt.Fprintf(stderr, "audit: %v\n", err)
			return 1
		}
		for _, f := range findings {
			fmt.Fprintf(stdout, "[%s] id=%d lifecycle=%s reasons=%v title=%q\n",
				a.name, f.EntryID, f.Current, f.Reasons, f.Title)
		}
		total += len(findings)
	}
	fmt.Fprintf(stdout, "%d finding(s)\n", total)
	return 0
}

type namedAudit struct {
	name string
	run  func() ([]audit.Finding, error)
}

func selectAudits(check string, svc *audit.Service, now time.Time) ([]namedAudit, bool) {
	all := []namedAudit{
		{"outdated", svc.OutdatedCandidates},
		{"canonical", svc.CanonicalCandidates},
		{"canonical-health", svc.CanonicalHealthIssues},
		{"supersession", svc.SupersessionIssues},
		{"age", func() ([]audit.Finding, error) { return svc.AgeCandidates(now) }},
	}
	if check == "all" {
		return all, true
	}
	for _, a := range all {
		if a.name == check {
			return []namedAudit{a}, true
		}
	}
	return nil, false
}
