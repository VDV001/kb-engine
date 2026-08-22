package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/botinbox"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

// runAdd puts one entry into the catalog: either the owner's own artefact — a
// standard, a write-up, a draft, identified by the file it lives in — or
// someone else's article, identified by its address.
//
// Until this command existed, the engine could only ingest a bot inbox, which
// keys entries by url and drops whatever has none. The consequence was measured
// rather than suspected: three standards existed as files and were absent from
// the catalog entirely.
//
// The second half — --url — closes the mirror image of that hole, measured the
// same way. A digest article carries the owner's own verdict, description and
// category, and neither door could write it: `add` demanded a file, `inbox`
// hardcodes source=bot-inbox and derives the category from a hub table. So the
// digest was written by editing catalog.json by hand, past every check the
// engine has — and that is how a category outside meta.categories got in and
// lived for fifteen days.
func runAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	title := fs.String("title", "", "entry title")
	category := fs.String("category", "", "catalog category, e.g. standards")
	file := fs.String("file", "", "path to the artefact, relative to the knowledge base")
	url := fs.String("url", "", "address of someone else's material (instead of --file)")
	verdict := fs.String("verdict", "", "triage verdict for someone else's material: keep|consider|skip|skip-unavailable")
	author := fs.String("author", "", "author of someone else's material")
	description := fs.String("description", "", "one or two sentences on what this is")
	tags := fs.String("tags", "", "comma-separated tags")
	version := fs.String("version", "", "semver of the artefact, e.g. 1.3.0")
	lifecycle := fs.String("lifecycle", "active", "lifecycle: active|outdated|canonical|superseded|dead-end")
	source := fs.String("source", "internal", "where the entry came from, e.g. internal or digest")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if code := checkAddFlags(*catalogPath, *title, *category, *file, *url, stderr); code != 0 {
		return code
	}

	params, err := artefactParams(entryFlags{
		title: *title, category: *category, file: *file, url: *url,
		verdict: *verdict, author: *author, description: *description,
		tags: *tags, version: *version, lifecycle: *lifecycle, source: *source,
	})
	if err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 2
	}

	cat, err := catalogjson.Load(*catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}

	// Планировщик выбирается тем, чем запись опознаётся: файлом или адресом.
	// Дедуп у них разный, и подменить один другим значило бы пропустить повтор.
	plan := ingest.PlanArtefacts
	identity := *file
	if *file == "" {
		plan = ingest.Plan
		identity = *url
	}
	added, rep, err := plan(cat, []domain.EntryParams{params}, time.Now())
	if err != nil {
		var undeclared *ingest.UndeclaredCategoryError
		if errors.As(err, &undeclared) {
			fmt.Fprintf(stderr, "add: %v\n", categoryRefusal(undeclared))
			return 2
		}
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	if rep.CategoriesUnchecked {
		// Правило 11: инструмент обязан называть, чего он НЕ проверил.
		fmt.Fprintln(stdout, "add: категорию не проверял — каталог не объявляет ни одной в meta.categories")
	}
	if rep.Added == 0 {
		// Not an error and not a success: the entry is already in the catalog,
		// and saying so beats both a silent zero and a failure exit code.
		fmt.Fprintf(stdout, "add: %s is already in the catalog — nothing added\n", identity)
		return 0
	}
	if err := catalogjson.AppendEntries(*catalogPath, added); err != nil {
		fmt.Fprintf(stderr, "add: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added id=%d %q\n", added[0].ID(), added[0].Title())
	return 0
}

// categoryRefusal turns the use case's refusal into a sentence that says what to
// write instead. The neighbour is found with the same edit distance the fin
// commands use for flags — the engine already applies this to data (an unknown
// spelling of a place is brought to the known one and the substitution is said
// out loud), and this is the same move applied to its own interface.
func categoryRefusal(e *ingest.UndeclaredCategoryError) string {
	msg := fmt.Sprintf("категория %q не объявлена в meta.categories", e.Category)
	if near := nearestName(e.Category, e.Declared); near != "" {
		return msg + fmt.Sprintf(" — похоже на %q", near)
	}
	if len(e.Declared) > 0 {
		return msg + fmt.Sprintf(" — объявлены: %s", strings.Join(e.Declared, ", "))
	}
	return msg
}

// entryFlags carries what the command was told, so the builder below reads as a
// list of decisions rather than a line of eleven positional strings.
type entryFlags struct {
	title, category, file, url         string
	verdict, author, description, tags string
	version, lifecycle, source         string
}

// artefactParams builds the domain params, asking the domain about every value
// that has a domain type. A bad category or lifecycle stops here, before the
// catalog is even read.
func artefactParams(f entryFlags) (domain.EntryParams, error) {
	cat, err := domain.NewCategory(f.category)
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("--category: %w", err)
	}
	life, err := domain.NewLifecycle(f.lifecycle)
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("--lifecycle: %w", err)
	}

	p := domain.EntryParams{
		Kind:        domain.KindArticle,
		Title:       f.title,
		Category:    cat,
		Lifecycle:   life,
		URL:         f.url,
		Author:      f.author,
		Description: f.description,
		Source:      f.source,
		Tags:        splitList(f.tags),
	}

	if f.file != "" {
		notes, err := domain.NewNotesPath(f.file)
		if err != nil {
			return domain.EntryParams{}, fmt.Errorf("--file: %w", err)
		}
		p.NotesFile = notes.String()
	}
	// Номер статьи есть в адресе, и не перенести его — значит потерять то, что
	// уже известно. Адрес не с Хабра номера не получает: пустое поле честнее
	// выдуманного.
	if id := botinbox.HabrIDFromURL(f.url); id != 0 {
		p.HabrID = &id
	}

	read, verdict, err := triageOf(f.file, f.verdict)
	if err != nil {
		return domain.EntryParams{}, err
	}
	p.ReadState, p.Verdict = read, verdict
	if f.version != "" {
		v, err := domain.NewVersion(f.version)
		if err != nil {
			return domain.EntryParams{}, fmt.Errorf("--version: %w", err)
		}
		p.Version = &v
	}
	return p, nil
}

// checkAddFlags reports the first thing missing from the command line, or 0 when
// the command may proceed. It is separate from runAdd because the flags answer
// three different questions: what to write into, what to write, and how the
// entry is identified.
func checkAddFlags(catalogPath, title, category, file, url string, stderr io.Writer) int {
	for name, value := range map[string]string{"--catalog": catalogPath, "--title": title, "--category": category} {
		if value == "" {
			fmt.Fprintf(stderr, "add: %s is required\n", name)
			return 2
		}
	}
	// Идентичность записи — файл или адрес. Без обоих движок не знает, что он
	// добавляет, и дедуп ему не на чем построить.
	if strings.TrimSpace(file) == "" && strings.TrimSpace(url) == "" {
		fmt.Fprintln(stderr, "add: нужен --file (свой артефакт) или --url (чужой материал) — идентичность записи одна из двух")
		return 2
	}
	if file != "" {
		if err := checkArtefactExists(catalogPath, file); err != nil {
			fmt.Fprintf(stderr, "add: --file: %v\n", err)
			return 2
		}
	}
	return 0
}

// triageOf decides the reading state and the verdict.
//
// An own artefact is read by definition — it was written here. Someone else's
// material is read once it has a verdict, and undecided otherwise: inventing a
// verdict would record a decision the human never made.
func triageOf(file, verdict string) (*domain.ReadState, *domain.Verdict, error) {
	state := "read"
	if file == "" && verdict == "" {
		state = "unread"
	}
	read, err := domain.NewReadState(state)
	if err != nil {
		return nil, nil, err
	}
	if verdict == "" {
		return &read, nil, nil
	}
	v, err := domain.NewVerdict(verdict)
	if err != nil {
		return nil, nil, fmt.Errorf("--verdict: %w", err)
	}
	return &read, &v, nil
}
