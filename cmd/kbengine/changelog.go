package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/daniil/kb-engine/internal/adapter/changelog"
)

// runChangelog parses a Keep-a-Changelog markdown file and writes the structured
// changelog.json the dashboard consumes.
func runChangelog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("changelog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "path to CHANGELOG.md")
	out := fs.String("out", "", "path to write changelog.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" || *out == "" {
		fmt.Fprintln(stderr, "changelog: --in and --out are required")
		return 2
	}

	md, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(stderr, "changelog: %v\n", err)
		return 1
	}

	doc := changelog.Parse(string(md))
	compact, err := json.Marshal(doc)
	if err != nil {
		fmt.Fprintf(stderr, "changelog: %v\n", err)
		return 1
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		fmt.Fprintf(stderr, "changelog: %v\n", err)
		return 1
	}
	pretty.WriteByte('\n')
	if err := os.WriteFile(*out, pretty.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "changelog: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "changelog: %s -> %s (%d release(s), current %s)\n",
		*in, *out, len(doc.Releases), doc.CurrentVersion)
	return 0
}
