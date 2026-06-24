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
		fmt.Fprintln(stderr, "usage: kbengine <command> [flags]\ncommands: audit")
		return 2
	}
	switch args[0] {
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func runAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "audit: --catalog is required")
		return 2
	}

	svc := audit.NewService(catalogjson.FileLoader{Path: *catalogPath})
	findings, err := svc.OutdatedCandidates()
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return 1
	}

	for _, f := range findings {
		fmt.Fprintf(stdout, "id=%d lifecycle=%s reasons=%v title=%q\n",
			f.EntryID, f.Current, f.Reasons, f.Title)
	}
	fmt.Fprintf(stdout, "%d outdated candidate(s)\n", len(findings))
	return 0
}
