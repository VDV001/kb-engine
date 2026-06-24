package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/tasklist"
	"github.com/daniil/kb-engine/internal/usecase/taskaudit"
)

// runAuditTasks runs the ADR-015 task↔catalog consistency check. It reads the
// task list from stdin (plaintext by default, JSON with --json) and exits 1 if
// any orphan is found, 2 on a usage/parse error, 0 when clean.
func runAuditTasks(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit-tasks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	asJSON := fs.Bool("json", false, "parse stdin as a JSON task list instead of plaintext")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "audit-tasks: --catalog is required")
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "audit-tasks: read stdin: %v\n", err)
		return 2
	}
	if strings.TrimSpace(string(raw)) == "" {
		fmt.Fprintln(stderr, "audit-tasks: empty stdin (paste the task list)")
		return 2
	}

	tasks, err := parseTasks(string(raw), *asJSON)
	if err != nil {
		fmt.Fprintf(stderr, "audit-tasks: %v\n", err)
		return 2
	}

	cat, err := catalogjson.Load(*catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "audit-tasks: %v\n", err)
		return 2
	}

	res := taskaudit.Audit(cat, tasks)
	printAuditReport(stdout, tasks, res)
	if res.HasOrphans() {
		return 1
	}
	return 0
}

func parseTasks(raw string, asJSON bool) ([]taskaudit.Task, error) {
	if asJSON {
		return tasklist.ParseJSON(raw)
	}
	return tasklist.ParsePlain(raw)
}

func printAuditReport(stdout io.Writer, tasks []taskaudit.Task, res taskaudit.Result) {
	withHabr := 0
	for _, t := range tasks {
		if t.HabrID != "" {
			withHabr++
		}
	}
	fmt.Fprintln(stdout, "=== Task -> Catalog Audit (ADR-015) ===")
	fmt.Fprintf(stdout, "Tasks parsed:                  %d\n", len(tasks))
	fmt.Fprintf(stdout, "Of them with habr-id:          %d\n", withHabr)
	fmt.Fprintf(stdout, "  consistent (done + in cat):  %d\n", len(res.Consistent))
	fmt.Fprintf(stdout, "  pending but in catalog:      %d\n", len(res.PendingPresent))
	fmt.Fprintf(stdout, "  ORPHANS (done without cat):  %d\n", len(res.Orphans))
	for _, t := range res.Orphans {
		fmt.Fprintf(stdout, "    [ORPHAN] task #%s status=%s habr=%s\n", t.ID, t.Status, t.HabrID)
	}
	for _, t := range res.PendingPresent {
		fmt.Fprintf(stdout, "    [CLOSE?] task #%s habr=%s is already in catalog\n", t.ID, t.HabrID)
	}
}
