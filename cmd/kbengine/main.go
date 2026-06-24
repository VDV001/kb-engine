// Command kbengine is the CLI entry point for the KB engine.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns the process exit code. It takes its
// I/O as parameters so it is testable without touching os globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kbengine <command> [flags]\ncommands: audit, dedup")
		return 2
	}
	switch args[0] {
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "dedup":
		return runDedup(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
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
	selected, ok := selectAudits(*check, svc)
	if !ok {
		fmt.Fprintf(stderr, "audit: unknown --check %q (want outdated|canonical|supersession|all)\n", *check)
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

func selectAudits(check string, svc *audit.Service) ([]namedAudit, bool) {
	all := []namedAudit{
		{"outdated", svc.OutdatedCandidates},
		{"canonical", svc.CanonicalCandidates},
		{"supersession", svc.SupersessionIssues},
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
