// Command kbengine is the CLI entry point for the KB engine.
package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	root "github.com/daniil/kb-engine"
	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/artefactfs"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// version is the build version. It defaults to "dev" and is overridden at
// release time via -ldflags "-X main.version=<tag>" (see .goreleaser.yaml).
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns the process exit code. It takes its
// I/O as parameters so it is testable without touching os globals.
// commands maps a verb to its handler. A table rather than a switch: every new
// command was making the dispatcher itself more complex, though dispatching
// never got harder.
var commands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"set":         runSet,
	"audit":       runAudit,
	"audit-tasks": func(a []string, o, e io.Writer) int { return runAuditTasks(a, os.Stdin, o, e) },
	"changelog":   runChangelog,
	"dedup":       runDedup,
	"drift":       runDrift,
	"fin":         runFin,
	"inbox":       runInbox,
	"migrate":     runMigrate,
	"serve":       runServe,
	"tui":         runTUI,
	"version":     func(_ []string, o, _ io.Writer) int { return runVersion(o) },
}

// usageLine lists the verbs in a stable order, so the help text does not
// reshuffle itself between runs the way a map iteration would.
func usageLine() string {
	verbs := slices.Sorted(maps.Keys(commands))
	return "usage: kbengine <command> [flags]\ncommands: " + strings.Join(verbs, ", ")
}

// run dispatches a subcommand and returns the process exit code. It takes its
// I/O as parameters so it is testable without touching os globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageLine())
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:], stdout, stderr)
}

// buildInfo — версия, коммит и время сборки текущего бинаря. Одно место на
// весь процесс: их печатает `kbengine version` и их же отдаёт /api/engine,
// и расходиться этим двум ответам не с чего.
func buildInfo() httpapi.EngineInfo {
	e := httpapi.EngineInfo{Version: version}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return e
	}
	if e.Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		e.Version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			e.Commit = s.Value
		case "vcs.time":
			e.Built = s.Value
		}
	}
	return e
}

// runVersion prints the build version, plus VCS revision and build time when
// the binary carries Go module build info (e.g. installed via `go install`).
func runVersion(stdout io.Writer) int {
	e := buildInfo()
	fmt.Fprintf(stdout, "kbengine %s\n", e.Version)
	if e.Commit != "" {
		fmt.Fprintf(stdout, "commit: %s\n", e.Commit)
	}
	if e.Built != "" {
		fmt.Fprintf(stdout, "built:  %s\n", e.Built)
	}
	return 0
}

// changelogWarning — текст предупреждения, когда из указанного файла не вышло
// ни одного релиза. Пустая строка означает «всё в порядке».
//
// Молчать здесь нельзя: пустой разбор доезжает до страницы как «v0.0.0 · —»,
// то есть выглядит фактом о базе, а не сообщением о том, что движок не понял
// файл. Ошибкой это тоже не сделать — у молодого проекта CHANGELOG.md без
// релизов законен, и падать на нём значит требовать релиз ради запуска.
func changelogWarning(path string, releases int) string {
	if releases > 0 {
		return ""
	}
	msg := fmt.Sprintf("changelog: в %s не нашлось ни одного релиза — «Что нового» будет пустым", path)
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		msg += "\n  --changelog ждёт CHANGELOG.md (сам markdown), а не changelog.json, собранный из него"
	}
	return msg
}

// runTUI opens the catalog in the terminal. It is the second face on the same
// use cases the dashboard serves — reading goes through the same query service,
// so the two surfaces cannot disagree about what the catalog says.
func runTUI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "tui: --catalog is required")
		return 2
	}
	svc := query.NewService(catalogjson.FileLoader{Path: *catalogPath})
	if err := tui.Run(svc, catalogWriter{path: *catalogPath}, os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "tui: %v\n", err)
		return 1
	}
	return 0
}

// catalogWriter turns an edit made on a card into the same call the set command
// makes. One write path for both surfaces: a second one would eventually apply
// different rules to the same file.
type catalogWriter struct{ path string }

func (w catalogWriter) Save(e tui.Edit) error {
	_, err := catalogjson.SetFields(w.path, []int{e.ID}, catalogjson.Changes{
		Lifecycle: e.Lifecycle,
		Verdict:   e.Verdict,
	})
	return err
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	configPath := fs.String("analytics-config", "", "optional path to analytics_config.json (semantic layer)")
	ledgerPath := fs.String("ledger", "", "optional path to transactions.jsonl (enables the finances view)")
	workbookPath := fs.String("from", "", "optional path to Учёт_финансов.xlsx (account balances)")
	changelogPath := fs.String("changelog", "", "optional path to CHANGELOG.md («Что нового» in Settings)")
	nowPath := fs.String("now", "", "optional path to active-pipeline.md (the Now view)")
	teamPath := fs.String("team", "", "optional path to team.json (the Team view)")
	projectsPath := fs.String("projects", "", "optional path to projects.json (the Projects view)")
	mediaPath := fs.String("media", "", "optional path to a directory of the owner's images, served at /media/")
	// Loopback by default. With --ledger this process serves four years of
	// personal transactions with places, notes and balances; ":8080" would hand
	// them to anyone on the network. Binding wider stays possible, but as a
	// choice someone makes rather than one they inherit.
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "serve: --catalog is required")
		return 2
	}
	// The workbook holds account balances, and balances are only ever shown beside
	// ledger rows. Without --ledger there is nothing to attach them to, so the
	// flag used to be taken and dropped: /api/finances answered 200 with no
	// balances and nothing said the file had been ignored. A flag that cannot take
	// effect is a mistake in the command, so it stops here instead of at the point
	// where someone wonders why the balances are missing.
	if *workbookPath != "" && *ledgerPath == "" {
		fmt.Fprintln(stderr, "serve: --from needs --ledger (the workbook holds account balances; the rows come from the ledger)")
		return 2
	}

	handler, err := buildServeHandler(*catalogPath, *configPath, *ledgerPath, *workbookPath, *changelogPath, *nowPath, *teamPath, *projectsPath, *mediaPath)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	// Разбор changelog проверяется на старте, а не при первом запросе: увидеть
	// предупреждение в момент запуска можно, а поймать его посреди работы —
	// уже нет, потому что смотрят в этот момент на страницу, а не в терминал.
	if *changelogPath != "" {
		if raw, err := os.ReadFile(*changelogPath); err == nil {
			if w := changelogWarning(*changelogPath, len(changelog.Parse(string(raw)).Releases)); w != "" {
				fmt.Fprintln(stderr, w)
			}
		}
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(stdout, "kbengine: serving dashboard on %s (catalog %s)\n", *addr, *catalogPath)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// ledgerFinances reads the two sources the Finances view needs. The rows live
// in the ledger; the account balances exist only in the workbook, so a
// deployment can have rows and no balances (workbook path empty) but not the
// other way round.
//
// Both files are read per request rather than cached: they are edited by hand
// while the dashboard is open, and a stale balance is worse than a re-read.
type ledgerFinances struct{ ledgerPath, workbookPath string }

func (f ledgerFinances) Finances() (httpapi.Finances, error) {
	recs, err := financejsonl.Load(f.ledgerPath, time.Now)
	if err != nil {
		return httpapi.Finances{}, err
	}
	out := httpapi.Finances{Transactions: make([]domain.Transaction, 0, len(recs))}
	for _, r := range recs {
		out.Transactions = append(out.Transactions, r.Transaction())
	}
	if f.workbookPath != "" {
		led, err := financexlsx.Read(f.workbookPath, time.Now)
		if err != nil {
			return httpapi.Finances{}, err
		}
		out.Accounts = led.Accounts
	}
	return out, nil
}

// Summary totals the months asked for. Re-reads per request for the same reason
// Finances does, and takes the period as an argument rather than handing the
// whole history over: that keeps the arithmetic on this side of the wire.
func (f ledgerFinances) Summary(months []string) (finance.Summary, error) {
	recs, err := financejsonl.Load(f.ledgerPath, time.Now)
	if err != nil {
		return finance.Summary{}, err
	}
	return finance.Summarize(finance.Match(recs, finance.Filter{Months: months})), nil
}

func buildServeHandler(catalogPath, configPath, ledgerPath, workbookPath, changelogPath, nowPath, teamPath, projectsPath, mediaPath string) (http.Handler, error) {
	loader := catalogjson.FileLoader{Path: catalogPath}
	front, err := root.Frontend()
	if err != nil {
		return nil, err
	}
	// Перечитывается на каждый запрос — как и каталог. Отсутствие пути — не
	// ошибка, а пустой семантический слой; падение при старте проверяет, что
	// файл хотя бы читается сейчас.
	cfg := func() (analyticsconfig.Config, error) { return analyticsconfig.Config{}, nil }
	if configPath != "" {
		if _, err := analyticsconfig.Load(configPath); err != nil {
			return nil, err
		}
		cfg = func() (analyticsconfig.Config, error) { return analyticsconfig.Load(configPath) }
	}
	// Nil, not an empty struct: the handler distinguishes "no ledger configured"
	// from "ledger configured and unreadable", and only the second is an error.
	var fin httpapi.Financier
	if ledgerPath != "" {
		// Read once here for the same reason the analytics config is: the file is
		// re-read per request, so this proves nothing about later reads, but it
		// turns a typo in --from from a 500 on one tab into a refusal to start.
		// Without it the engine printed its usual serving line and answered every
		// other view, and the mistake only surfaced as «finances unavailable».
		if workbookPath != "" {
			if _, err := financexlsx.Read(workbookPath, time.Now); err != nil {
				// Names the flag and nothing else: the reader already says "open
				// workbook" and the path, and a third layer of prose around it
				// reads worse than the failure it describes.
				return nil, fmt.Errorf("--from: %w", err)
			}
		}
		fin = ledgerFinances{ledgerPath: ledgerPath, workbookPath: workbookPath}
	}
	docs, err := buildDocuments(nowPath, teamPath, projectsPath, mediaPath)
	if err != nil {
		return nil, err
	}

	var chlog httpapi.ChangelogLoader
	if changelogPath != "" {
		if _, err := os.ReadFile(changelogPath); err != nil {
			return nil, fmt.Errorf("changelog: %w", err)
		}
		chlog = func() (changelog.Document, error) {
			raw, err := os.ReadFile(changelogPath)
			if err != nil {
				return changelog.Document{}, err
			}
			return changelog.Parse(string(raw)), nil
		}
	}
	return httpapi.NewServer(query.NewService(loader), audit.NewService(loader),
		analytics.NewService(loader), fin, cfg, chlog, docs, buildInfo(), front), nil
}

// buildDocuments wires the owner's personal views. Each path is optional;
// a configured one is read once at startup so a typo fails at serve time,
// then re-read per request so edits show up on reload.
func buildDocuments(nowPath, teamPath, projectsPath, mediaPath string) (httpapi.Documents, error) {
	var docs httpapi.Documents
	if nowPath != "" {
		if _, err := os.ReadFile(nowPath); err != nil {
			return httpapi.Documents{}, fmt.Errorf("now: %w", err)
		}
		docs.Now = func() (string, error) {
			raw, err := os.ReadFile(nowPath)
			return string(raw), err
		}
	}
	fileJSON := func(name, path string) (func() ([]byte, error), error) {
		if _, err := os.ReadFile(path); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return func() ([]byte, error) { return os.ReadFile(path) }, nil
	}
	if teamPath != "" {
		var err error
		if docs.Team, err = fileJSON("team", teamPath); err != nil {
			return httpapi.Documents{}, err
		}
	}
	if projectsPath != "" {
		var err error
		if docs.Projects, err = fileJSON("projects", projectsPath); err != nil {
			return httpapi.Documents{}, err
		}
	}
	// Каталог проверяется тем же правилом, что и файлы выше: опечатка в пути
	// должна падать при старте, а не превращаться в страницу с пустыми
	// рамками вместо скриншотов.
	if mediaPath != "" {
		info, err := os.Stat(mediaPath)
		if err != nil {
			return httpapi.Documents{}, fmt.Errorf("media: %w", err)
		}
		if !info.IsDir() {
			return httpapi.Documents{}, fmt.Errorf("media: %s is not a directory", mediaPath)
		}
		docs.Media = os.DirFS(mediaPath)
	}
	return docs, nil
}

func runDedup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dedup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "dedup: --catalog is required")
		return 2
	}

	svc := audit.NewService(catalogjson.FileLoader{Path: *catalogPath})
	groups, err := svc.Duplicates()
	if err != nil {
		fmt.Fprintf(stderr, "dedup: %v\n", err)
		return 1
	}
	for _, g := range groups {
		fmt.Fprintf(stdout, "[%s] ids=%v key=%q\n", g.Kind, g.EntryIDs, g.Key)
	}
	fmt.Fprintf(stdout, "%d duplicate group(s)\n", len(groups))
	return 0
}

func runAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	check := fs.String("check", "all", "which audit to run: outdated|canonical|canonical-health|supersession|integrity|versions|batch|links|age|all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "audit: --catalog is required")
		return 2
	}

	svc := audit.NewService(catalogjson.FileLoader{Path: *catalogPath})
	// Artefact paths in the catalog are relative to the KB root, which is the
	// parent of the _data directory the catalog lives in.
	svc.WithArtefactVersions(artefactfs.Reader{Root: filepath.Dir(filepath.Dir(*catalogPath))})
	selected, ok := selectAudits(*check, svc, time.Now())
	if !ok {
		fmt.Fprintf(stderr, "audit: unknown --check %q (want outdated|canonical|canonical-health|supersession|integrity|versions|batch|links|age|all)\n", *check)
		return 2
	}

	total := 0
	for _, a := range selected {
		findings, err := a.run()
		if err != nil {
			fmt.Fprintf(stderr, "audit: %v\n", err)
			return 1
		}
		for _, f := range findings {
			fmt.Fprintf(stdout, "[%s] id=%d lifecycle=%s reasons=%v title=%q\n",
				a.name, f.EntryID, f.Current, f.Reasons, f.Title)
		}
		total += len(findings)
	}
	fmt.Fprintf(stdout, "%d finding(s)\n", total)

	// Link coverage is a different genre: it is not a lifecycle decision but a
	// statement about what the base has not looked at. Listing 527 entries here
	// would bury the findings that need one, so --check all says it in a line.
	if *check == "all" {
		if s := linkCoverageLine(svc, time.Now()); s != "" {
			fmt.Fprintln(stdout, s)
		}
	}
	return 0
}

// linkCoverageLine summarises what the base does not know about its own links.
// An empty string means every link was checked recently — the only case where
// saying nothing is honest.
func linkCoverageLine(svc *audit.Service, now time.Time) string {
	findings, err := svc.UncheckedLinkIssues(now)
	if err != nil || len(findings) == 0 {
		return ""
	}
	never, stale := 0, 0
	for _, f := range findings {
		if strings.Contains(strings.Join(f.Reasons, " "), "ни разу") {
			never++
			continue
		}
		stale++
	}
	return fmt.Sprintf("ссылки: %d не проверялись ни разу, %d проверялись больше двух месяцев назад — kbengine audit --check links",
		never, stale)
}

type namedAudit struct {
	name string
	run  func() ([]audit.Finding, error)
}

func selectAudits(check string, svc *audit.Service, now time.Time) ([]namedAudit, bool) {
	all := []namedAudit{
		{"outdated", svc.OutdatedCandidates},
		{"canonical", svc.CanonicalCandidates},
		{"canonical-health", svc.CanonicalHealthIssues},
		{"supersession", svc.SupersessionIssues},
		{"integrity", svc.IntegrityIssues},
		{"versions", svc.VersionDriftIssues},
		{"batch", svc.BatchConsistencyIssues},
		{"age", func() ([]audit.Finding, error) { return svc.AgeCandidates(now) }},
	}
	if check == "all" {
		return all, true
	}
	if check == "links" {
		return []namedAudit{{"links", func() ([]audit.Finding, error) { return svc.UncheckedLinkIssues(now) }}}, true
	}
	for _, a := range all {
		if a.name == check {
			return []namedAudit{a}, true
		}
	}
	return nil, false
}

// runSet edits entries already in the catalog. Until it existed the engine could
// only append: every change to a lifecycle, a tag or a link went through a
// script that read the file into a map and wrote it back, which is precisely how
// fields the domain does not model get lost quietly.
//
// Flags mirror what the catalog is actually curated with. --status is absent on
// purpose: the legacy status field conflates verdict, read-state and publish
// stage, and a flag that writes it back as one string would undo the split the
// loader performs on read.
func runSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	ids := fs.String("ids", "", "comma-separated entry ids to change")
	lifecycle := fs.String("lifecycle", "", "new lifecycle: active|outdated|canonical|superseded|dead-end")
	addTags := fs.String("add-tag", "", "comma-separated tags to add")
	removeTags := fs.String("remove-tag", "", "comma-separated tags to remove")
	related := fs.String("related", "", "comma-separated ids replacing related_ids (empty list clears it: --related=)")
	version := fs.String("version", "", "semver of an own artefact, e.g. 1.5.1 (clears revision)")
	revision := fs.Int("revision", 0, "edition counter of a card for someone else's material (clears version)")
	verdict := fs.String("verdict", "", "triage verdict: keep|consider|skip|skip-unavailable")
	notesFile := fs.String("file", "", "path to the write-up, relative to the knowledge base")
	sourceURL := fs.String("url", "", "http(s) address of the original material (--url= removes it)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "set: --catalog is required")
		return 2
	}
	parsed, err := parseIDs(*ids)
	if err != nil {
		fmt.Fprintf(stderr, "set: --ids: %v\n", err)
		return 2
	}

	ch := catalogjson.Changes{
		Lifecycle:  *lifecycle,
		AddTags:    splitList(*addTags),
		RemoveTags: splitList(*removeTags),
		Version:    *version,
		Revision:   *revision,
		Verdict:    *verdict,
		NotesFile:  *notesFile,
		URL:        *sourceURL,
	}
	// "--url=" passed with nothing after it is an instruction to remove the
	// address; "--url" not passed at all is not. Both look like "".
	if isFlagSet(fs, "url") && *sourceURL == "" {
		ch.ClearURL = true
	}
	// Distinguishes "--related was not passed" from "--related= was passed to
	// clear the list": both look like an empty string, and only the second is an
	// instruction.
	if isFlagSet(fs, "related") {
		ch.Related, err = parseIDs(*related)
		if err != nil {
			fmt.Fprintf(stderr, "set: --related: %v\n", err)
			return 2
		}
		if ch.Related == nil {
			ch.Related = []int{}
		}
	}

	n, err := catalogjson.SetFields(*catalogPath, parsed, ch)
	if err != nil {
		fmt.Fprintf(stderr, "set: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%d entry(ies) updated\n", n)
	return 0
}

func isFlagSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseIDs(s string) ([]int, error) {
	var out []int
	for _, p := range splitList(s) {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not an entry id", p)
		}
		out = append(out, n)
	}
	return out, nil
}
