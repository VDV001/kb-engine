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
		fmt.Fprintln(stderr, "usage: kbengine migrate <what> [flags]\nwhat: versions")
		return 2
	}
	switch args[0] {
	case "versions":
		return runMigrateVersions(args[1:], stdout, stderr)
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
