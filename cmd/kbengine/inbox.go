package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/botinbox"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

// runInbox ingests bot inbox *.json files into the catalog: decode → map →
// dedup/allocate ids → append. Decoded files are moved into --processed (when
// given) so they are not re-ingested.
func runInbox(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	inboxDir := fs.String("inbox", "", "directory of bot inbox *.json files")
	processedDir := fs.String("processed", "", "optional directory to move processed files into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "inbox: --catalog is required")
		return 2
	}
	if *inboxDir == "" {
		fmt.Fprintln(stderr, "inbox: --inbox is required")
		return 2
	}

	files, err := filepath.Glob(filepath.Join(*inboxDir, "*.json"))
	if err != nil {
		fmt.Fprintf(stderr, "inbox: %v\n", err)
		return 1
	}
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Fprintln(stdout, "inbox: nothing to do (no *.json in inbox)")
		return 0
	}

	cat, err := catalogjson.Load(*catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "inbox: %v\n", err)
		return 1
	}

	now := time.Now()
	params, processed := collectInboxParams(files, now, stderr)

	added, rep, err := ingest.Plan(cat, params, now)
	if err != nil {
		fmt.Fprintf(stderr, "inbox: %v\n", err)
		return 1
	}
	if err := persistIngest(*catalogPath, added, processed, *processedDir, now); err != nil {
		fmt.Fprintf(stderr, "inbox: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "inbox: added %d, skipped %d duplicate(s), %d without url\n",
		rep.Added, rep.SkippedDuplicate, rep.SkippedNoURL)
	return 0
}

// collectInboxParams decodes each file and maps its articles to id-less params.
// Files that fail to decode, and individual articles that fail to map, are
// reported to stderr and skipped; every successfully decoded file is returned
// as processed (eligible to be moved).
func collectInboxParams(files []string, now time.Time, stderr io.Writer) ([]domain.EntryParams, []string) {
	var params []domain.EntryParams
	var processed []string
	for _, f := range files {
		arts, err := decodeInboxFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "inbox: skipping %s: %v\n", filepath.Base(f), err)
			continue
		}
		for _, a := range arts {
			p, err := botinbox.MapArticle(a, now)
			if err != nil {
				fmt.Fprintf(stderr, "inbox: skipping article %q: %v\n", a.Title, err)
				continue
			}
			params = append(params, p)
		}
		processed = append(processed, f)
	}
	return params, processed
}

// persistIngest appends new entries to the catalog and moves processed files
// into processedDir when one is configured.
func persistIngest(catalogPath string, added []domain.Entry, processed []string, processedDir string, now time.Time) error {
	if len(added) > 0 {
		if err := catalogjson.AppendEntries(catalogPath, added); err != nil {
			return err
		}
	}
	if processedDir != "" {
		if err := moveProcessed(processed, processedDir, now); err != nil {
			return err
		}
	}
	return nil
}

func decodeInboxFile(path string) ([]botinbox.Article, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return botinbox.DecodeArticles(f)
}

// moveProcessed relocates files into dir, prefixing each name with a timestamp
// to avoid collisions across runs.
func moveProcessed(files []string, dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create processed dir: %w", err)
	}
	stamp := now.Format("20060102_150405")
	for _, f := range files {
		dest := filepath.Join(dir, stamp+"_"+filepath.Base(f))
		if err := os.Rename(f, dest); err != nil {
			return fmt.Errorf("move %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}
