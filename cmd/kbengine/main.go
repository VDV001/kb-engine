// Command kbengine is the CLI entry point for the KB engine.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	root "github.com/daniil/kb-engine"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns the process exit code. It takes its
// I/O as parameters so it is testable without touching os globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kbengine <command> [flags]\ncommands: audit, audit-tasks, changelog, dedup, inbox, serve")
		return 2
	}
	switch args[0] {
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "audit-tasks":
		return runAuditTasks(args[1:], os.Stdin, stdout, stderr)
	case "changelog":
		return runChangelog(args[1:], stdout, stderr)
	case "dedup":
		return runDedup(args[1:], stdout, stderr)
	case "inbox":
		return runInbox(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	addr := fs.String("addr", ":8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "serve: --catalog is required")
		return 2
	}

	handler, err := buildServeHandler(*catalogPath)
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

func buildServeHandler(catalogPath string) (http.Handler, error) {
	loader := catalogjson.FileLoader{Path: catalogPath}
	front, err := root.Frontend()
	if err != nil {
		return nil, err
	}
	return httpapi.NewServer(query.NewService(loader), audit.NewService(loader), analytics.NewService(loader), front), nil
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
	check := fs.String("check", "all", "which audit to run: outdated|canonical|supersession|all")
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
