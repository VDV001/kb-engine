package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// runMigrate dispatches the one-off catalog migrations. They live behind their
// own verb rather than under `set`, which stays a targeted edit by id.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kbengine migrate <what> [flags]\nwhat: versions, urls, writeups")
		return 2
	}
	switch args[0] {
	case "versions":
		return runMigrateVersions(args[1:], stdout, stderr)
	case "urls":
		return runMigrateURLs(args[1:], stdout, stderr)
	case "writeups":
		return runMigrateWriteups(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "migrate: unknown target %q\n", args[0])
		return 2
	}
}

func runMigrateVersions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate versions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "migrate versions: --catalog is required")
		return 2
	}

	plan, err := catalogjson.MigrateVersions(*catalogPath, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "migrate versions: %v\n", err)
		return 1
	}

	// Entries only the owner can settle stop the run before anything is written.
	// Deciding them here would mean inventing a version.
	if len(plan.Undecidable) > 0 {
		fmt.Fprintf(stderr, "migrate versions: %d запис(и) требуют вашего решения, файл не тронут:\n", len(plan.Undecidable))
		for _, c := range plan.Undecidable {
			fmt.Fprintf(stderr, "  id=%d version=%q — %s\n      %s\n", c.ID, c.Stored, c.Because, c.Title)
		}
		fmt.Fprintln(stderr, "  поправьте их через kbengine set и запустите снова")
		return 1
	}

	if len(plan.Moved) == 0 {
		fmt.Fprintln(stdout, "migrate versions: нечего переносить")
		return 0
	}

	if !*apply {
		fmt.Fprintf(stdout, "migrate versions: %d запис(ей) переедут из version в revision (файл не тронут, для записи добавьте --apply)\n", len(plan.Moved))
		return 0
	}
	fmt.Fprintf(stdout, "migrate versions: %d запис(ей) переехали из version в revision\n", len(plan.Moved))
	return 0
}

// runMigrateURLs strips campaign tracking from the catalog's addresses.
func runMigrateURLs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate urls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "migrate urls: --catalog is required")
		return 2
	}

	changes, err := catalogjson.MigrateURLs(*catalogPath, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "migrate urls: %v\n", err)
		return 1
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "migrate urls: нечего чистить")
		return 0
	}

	// The resulting address is printed, not just a count: this rewrites what an
	// entry is, and it has to be readable before it happens.
	for _, c := range changes {
		fmt.Fprintf(stdout, "  id=%d\n      было:  %s\n      стало: %s\n", c.EntryID, c.From, c.To)
	}
	if !*apply {
		fmt.Fprintf(stdout, "migrate urls: %d адрес(ов) потеряют кампанейский хвост (файл не тронут, для записи добавьте --apply)\n", len(changes))
		return 0
	}
	fmt.Fprintf(stdout, "migrate urls: %d адрес(ов) очищено\n", len(changes))
	return 0
}
