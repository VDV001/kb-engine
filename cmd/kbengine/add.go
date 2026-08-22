package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

// runAdd puts one of the owner's own artefacts into the catalog: a standard, a
// write-up, a draft — anything that lives as a file in the knowledge base
// rather than as a link to someone else's page.
//
// Until this command existed, the engine could only ingest a bot inbox, which
// keys entries by url and drops whatever has none. The consequence was measured
// rather than suspected: three standards existed as files and were absent from
// the catalog entirely, so the version audit never checked them and the
// dashboard never counted them.
func runAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	title := fs.String("title", "", "entry title")
	category := fs.String("category", "", "catalog category, e.g. standards")
	file := fs.String("file", "", "path to the artefact, relative to the knowledge base")
	description := fs.String("description", "", "one or two sentences on what this is")
	tags := fs.String("tags", "", "comma-separated tags")
	version := fs.String("version", "", "semver of the artefact, e.g. 1.3.0")
	lifecycle := fs.String("lifecycle", "active", "lifecycle: active|outdated|canonical|superseded|dead-end")
	source := fs.String("source", "internal", "where the entry came from")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	for name, value := range map[string]string{"--catalog": *catalogPath, "--title": *title, "--category": *category, "--file": *file} {
		if value == "" {
			fmt.Fprintf(stderr, "add: %s is required\n", name)
			return 2
		}
	}

	if err := checkArtefactExists(*catalogPath, *file); err != nil {
		fmt.Fprintf(stderr, "add: --file: %v\n", err)
		return 2
	}

	params, err := artefactParams(*title, *category, *file, *description, *tags, *version, *lifecycle, *source)
	if err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 2
	}

	cat, err := catalogjson.Load(*catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	added, rep, err := ingest.PlanArtefacts(cat, []domain.EntryParams{params}, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if rep.Added == 0 {
		// Not an error and not a success: the file is already in the catalog,
		// and saying so beats both a silent zero and a failure exit code.
		fmt.Fprintf(stdout, "add: %s is already in the catalog — nothing added\n", *file)
		return 0
	}
	if err := catalogjson.AppendEntries(*catalogPath, added); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added id=%d %q\n", added[0].ID(), added[0].Title())
	return 0
}

// artefactParams builds the domain params, asking the domain about every value
// that has a domain type. A bad category or lifecycle stops here, before the
// catalog is even read.
func artefactParams(title, category, file, description, tags, version, lifecycle, source string) (domain.EntryParams, error) {
	cat, err := domain.NewCategory(category)
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("--category: %w", err)
	}
	life, err := domain.NewLifecycle(lifecycle)
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("--lifecycle: %w", err)
	}
	notes, err := domain.NewNotesPath(file)
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("--file: %w", err)
	}
	// An own artefact is read by definition — it was written here. The verdict
	// stays empty: it is a triage decision about someone else's material.
	read, err := domain.NewReadState("read")
	if err != nil {
		return domain.EntryParams{}, err
	}

	p := domain.EntryParams{
		Kind:        domain.KindArticle,
		Title:       title,
		Category:    cat,
		Lifecycle:   life,
		ReadState:   &read,
		NotesFile:   notes.String(),
		Description: description,
		Source:      source,
		Tags:        splitList(tags),
	}
	if version != "" {
		v, err := domain.NewVersion(version)
		if err != nil {
			return domain.EntryParams{}, fmt.Errorf("--version: %w", err)
		}
		p.Version = &v
	}
	return p, nil
}
