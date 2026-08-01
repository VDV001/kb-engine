package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// runMigrateWriteups turns every write-up that articles merely pointed at into
// an entry of its own. See catalogjson.MigrateWriteups for why the file member
// could not go on meaning two things at once.
func runMigrateWriteups(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate writeups", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "migrate writeups: --catalog is required")
		return 2
	}

	root := artefactRoot(*catalogPath)
	plan, err := catalogjson.MigrateWriteups(*catalogPath,
		func(file string) string { return writeupTitle(root, file) }, time.Now, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "migrate writeups: %v\n", err)
		return 1
	}

	if len(plan.Created) == 0 {
		fmt.Fprintln(stdout, "migrate writeups: нечего переносить")
		return 0
	}
	if !*apply {
		fmt.Fprintf(stdout, "migrate writeups: %d запис(ей) переедут с file на ссылку, разборов станет записями — %d (файл не тронут, для записи добавьте --apply)\n",
			plan.Moved, len(plan.Created))
		for _, w := range plan.Created {
			fmt.Fprintf(stdout, "  + id=%d ← %d запис(ей)  %s\n      %s\n", w.ID, w.Count, w.File, w.Title)
		}
		return 0
	}
	fmt.Fprintf(stdout, "migrate writeups: %d запис(ей) переехали на ссылку, заведено разборов — %d\n",
		plan.Moved, len(plan.Created))
	return 0
}

// writeupTitle reads the write-up's own heading. Falling back to the file name
// is deliberate: a note without a heading still needs a title a person can read
// in a list, and inventing one from the path is honest about where it came from.
func writeupTitle(root, file string) string {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return titleFromPath(file)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for i := 0; i < writeupHeadingLines && scanner.Scan(); i++ {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "# "); ok {
			if h := strings.TrimSpace(heading); h != "" {
				return h
			}
		}
	}
	return titleFromPath(file)
}

// titleFromPath turns notes/2026-05-01_habr-batch10-keep_v1.md into
// "2026-05-01 habr batch10 keep v1".
func titleFromPath(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), ".md")
	return strings.NewReplacer("_", " ", "-", " ").Replace(base)
}

// writeupHeadingLines bounds how far the heading is looked for. It sits at the
// top; scanning a whole document to find nothing is work no answer depends on.
const writeupHeadingLines = 20
