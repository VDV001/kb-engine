package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// runMigrate dispatches the one-off catalog migrations. They live behind their
// own verb rather than under `set`, which stays a targeted edit by id.
// migrateTargets maps a migration target to its handler. Реестр, а не switch:
// перечень целей в помощи собирается из его ключей и не может отстать от кода.
var migrateTargets = map[string]func(args []string, stdout, stderr io.Writer) int{
	"versions": runMigrateVersions,
	"urls":     runMigrateURLs,
	"writeups": runMigrateWriteups,
	"habr-ids": runMigrateHabrIDs,
}

func migrateUsageLine() string {
	return "usage: kbengine migrate <what> [flags]\nwhat: " +
		strings.Join(slices.Sorted(maps.Keys(migrateTargets)), ", ")
}

func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, migrateUsageLine())
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, migrateUsageLine())
		return 0
	}
	target, ok := migrateTargets[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "migrate: unknown target %q\n", args[0])
		return 2
	}
	return target(args[1:], stdout, stderr)
}

func runMigrateVersions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate versions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if code, stop := parseFlags(fs, args); stop {
		return code
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

// runMigrateHabrIDs проставляет номер статьи там, где он известен из адреса.
//
// Отдельная команда, а не часть аудита: аудит называет расхождения, а это
// разовый перенос уже известного значения в поле, где его ждут.
func runMigrateHabrIDs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate habr-ids", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "migrate habr-ids: --catalog is required")
		return 2
	}

	plan, err := catalogjson.MigrateHabrIDs(*catalogPath, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "migrate habr-ids: %v\n", err)
		return 1
	}

	// Расхождения называются всегда — и до записи, и после: движок не знает,
	// что верно, поле или адрес, и решать это не его дело.
	for _, c := range plan.Conflicts {
		fmt.Fprintf(stderr, "migrate habr-ids: #%d habr_id=%d, а в адресе %d — %s\n",
			c.EntryID, c.Stored, c.InURL, c.Title)
	}
	if len(plan.Conflicts) > 0 {
		fmt.Fprintf(stderr, "  %d расхожден(ий) оставлено как есть — решать вам\n", len(plan.Conflicts))
	}

	if len(plan.Filled) == 0 && len(plan.Normalized) == 0 {
		fmt.Fprintln(stdout, "migrate habr-ids: нечего заполнять")
		return 0
	}
	verb := "проставится/приведётся"
	tail := " (файл не тронут, для записи добавьте --apply)"
	if *apply {
		verb, tail = "проставлено/приведено", ""
	}
	fmt.Fprintf(stdout, "migrate habr-ids: %s — %d пустых заполняется из адреса, %d строковых приводится к числу%s\n",
		verb, len(plan.Filled), len(plan.Normalized), tail)
	return 0
}

// runMigrateURLs strips campaign tracking from the catalog's addresses.
func runMigrateURLs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate urls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	apply := fs.Bool("apply", false, "write the changes (without it the plan is printed and nothing is written)")
	if code, stop := parseFlags(fs, args); stop {
		return code
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
