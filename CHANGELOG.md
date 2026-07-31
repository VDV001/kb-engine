# Changelog

All notable changes to kb-engine are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and the
project adheres to [Semantic Versioning](https://semver.org/). (kb-engine can
parse this file itself: `kbengine changelog --in CHANGELOG.md --out changelog.json`.)

## [Unreleased]

## [0.4.0] — 2026-07-31

> The licence changes to AGPL-3.0-or-later with a commercial alternative, and
> the catalog stops hiding metadata inside its own content: translations,
> verdicts and tag names each move into a field of their own.

### Changed

- **Licence is now AGPL-3.0-or-later**, with a commercial alternative described
  in `COMMERCIAL-LICENSE.md`. Releases up to and including `0.3.0` were
  published under MIT and stay under MIT — that grant cannot be withdrawn after
  the fact. These terms apply from this release onward.
- The dashboard footer offers the source and names the licence. This is not
  decoration: AGPL §13 entitles anyone using the engine over a network to its
  source, and the engine is served over HTTP. A test keeps the link from being
  removed by a later markup edit.

### Added

- **Tag labels** — `meta.tag_labels` maps a tag key to a readable name, the way
  `meta.categories` already does for categories. Russian tags could be neither
  translated nor merged with their English synonyms while the key *was* the
  name; now `job-market` carries «Рынок труда» beside it. The dictionary is
  separate from the category one on purpose: a category label is
  «Name: description» and gets cut at the colon, a tag label is a name and a
  colon inside it belongs to the name.
- **Translation as a field** — `is_translation` replaces the `[Перевод]` prefix
  that used to live inside the title. Sixty entries carried it that way, and it
  would have stayed Russian through any interface translation, because it sat
  in content rather than in data. The archive shows a badge and filters on it.
- **Tag filter and a clickable tag cloud** — there was no way to filter by tag
  at all, with 3853 tags against 24 categories; search was the only route and it
  also matched titles and descriptions. Clicking a tag in the cloud now opens
  the archive filtered by it, as the original dashboard's caption promised. The
  picked tag shows as a removable chip: it has no selector of its own, and
  without the chip a narrowed result looked unexplained.
- **The projects page prints to PDF** — print styles rather than an engine
  command. A headless renderer would put Chromium next to a binary that has two
  direct dependencies and a 9 MB image. Details expand, cover gradients survive
  `print-color-adjust`, cards do not break across sheets, and links print with
  their address.
- **Summary view completed** — entry source, the changelog origin line, an
  expandable release history and the editorial section at the foot.

### Fixed

- `Ledger.Net` removed: its only caller was its own test, and the production
  sum runs through `report.go`. `NewServiceWithClock` was kept — it is a clock
  seam, not forgotten code.
- `npm run lint` no longer scans `frontend/dist`. It reported twelve warnings in
  a minified bundle against zero in the sources; CI had been calling
  `oxlint src` and was right. A linter that is always red stops being read.

## [0.3.0] — 2026-07-31

> The dashboard port lands: every view but Projects now reads from the engine,
> and the knowledge graph says something the numbers on it did not already say.

### Added

- **Dashboard ported 1:1** — KPI row, per-day bars, category donut, status
  bars, the 21-week activity strip, the tag cloud and the knowledge graph.
  - The donut is real: the original drew a ring of two fixed-width colours that
    was not tied to the data at all. Shares and percentages now come from the
    catalog.
  - The tag cloud is collapsed by default. Expanded it fills the screen, and it
    is read rarely.
  - The activity strip counts by `date_added`, where the original counted by
    `date_created` under a caption promising added entries. On this catalog
    those are different facts: 862 entries carry only `date_added`, 461 only
    `date_created`.
  - The second KPI is throughput over 30 days against the previous 30. It used
    to be "categories", which the donut below already answers; throughput was
    the one question no tile answered.
- **Knowledge graph: the ring now means closeness to the core.** It used to
  mean size — chosen for layout reasons, since the outer ring's longer arc
  keeps small boxes from colliding. That made the radius repeat what the node's
  count and area already said. Inner ring: topics fused with the core by shared
  vocabulary. Outer ring: islands with their own. Non-overlap is now a test,
  because the old ordering held it by accident and the new one does not.
- **Conclusions under the graph** — core, fused topics and islands, each with
  the tags the link is actually made of. Selection is by shared tags **per
  entry**, not by their absolute number: measured against the live catalog, the
  absolute figure merely tracks category size (95 shared tags for the largest,
  4 for the smallest), so the core came out connected to all 23 categories and
  no islands existed. Density separates them cleanly, from 3.4 down to 0.3.
- **Analytics brought to the original's layout** — manifesto cards with a
  coloured spine per thesis type, pattern cards with an N/12 reach bar,
  contradictions as two columns and a resolution, gap cards with priority and
  clusters, and a research-brief sidebar. Long blocks collapse while their
  heading and reach stay visible.
- **Archives ported 1:1** and **finance charts**, with the arithmetic moved
  server-side so it lives in one place.

### Fixed

- **`kbengine inbox` could not write the live catalog at all.** `AppendEntries`
  refused any file carrying an unknown top-level key, and the live catalog
  carries `last_updated`, written by the Python dashboard. The refusal was
  deliberate and aimed at not losing data, but in practice it protected nothing
  and blocked the command. Unknown keys are now carried through verbatim and in
  their original order — order is what keeps a rewrite out of the diff.
- **No cache headers were served**, so browsers held a stale `index.html`,
  which references the bundle by content hash. Rebuilt pages did not reach the
  reader. `assets/*` are immutable, `index.html` is `no-cache`.
- **Horizontal scroll on narrow screens**: a grid item does not shrink below
  its content without `min-w-0`, and the dashboard pushed the whole page to 497
  pixels in a 390-pixel viewport.
- Database health reports two honest fractions with their own denominators
  instead of one averaged score that meant nothing.

### Changed

- Catalog statuses are an English enum under future i18n (`consider` rather
  than a transliteration); 712 entries migrated, and the legacy Python
  dashboard was updated in the same step — it compared the status against a
  literal in five places.
- One loading hook instead of four copies, with an architectural gate keeping
  requests in `api.ts` and loading in `hooks/`.

### Removed

- `OverviewView` and `BarList`, unused once the dashboard landed.

## [0.2.0] — 2026-07-30

> A finance module, a design system with one source of colour for web and
> terminal, and content parity with the dashboard this engine replaces.

### Added

- **Finance module** — a second data domain beside the catalog, kept in a
  hand-maintained workbook the owner has four years of history in.
  - `Money` as exact kopecks: 125 of 507 expense rows carry decimals, and
    binary floating point cannot represent 0.1 exactly, so every total is
    reproducible by construction.
  - `Transaction` entity with injected clock, enforced invariants and domain
    errors; an income carries no account, category, subcategory or place,
    because the Доходы sheet has no column for any of them.
  - Two-way sync between the workbook and a newline-delimited JSON ledger,
    matched by stable ULIDs and stopping on conflict rather than guessing a
    winner. `fin import`, `fin add`, `fin list`, `fin report`, `fin sync`
    (`--init`, `--dry-run`, `--resolve`, `--migrate-ids`).
  - Ten rotating backups behind every write to either side, atomic saves, and
    a refusal to write under an open editor (LibreOffice and Excel).
  - `--account` on add, list and report: which account the money moved
    through, read from the workbook's own Счета vocabulary.
- **Design system** — `design/tokens.json` as the single source of colour,
  generated by `go generate` into both CSS custom properties and lipgloss
  styles, so the web view and the coming TUI cannot drift.
  - Fonts embedded in the binary (PT Serif, Inter, JetBrains Mono — all with
    Cyrillic coverage, which is why two earlier candidates were dropped).
  - A contrast gate over 13 colour pairs, checked in tests against WCAG.
  - Layout verified from 1920 down to 390 pixels.
- **Content parity with the Python dashboard** — every view now lives here.
  - **Archives** — the full catalog browser: status, filters, sort, page
    window, and `date_added` on every entry.
  - **Meta-analytics** — five tabs, with the tag graph computed on request via
    `/api/graph` rather than drawn by hand. Pull quotes, chains and both
    support shapes are modelled.
  - **Settings** — base info and «Что нового» behind `/api/changelog`.
  - **Now / Team / Projects** — rendered from the owner's own files
    (`--now`, `--team`, `--projects`), re-read on every request so nothing is
    baked into the binary. Only the renderer ships; anonymised fixtures live
    in `docs/examples/`.
  - **Finances** — ledger rows and account balances, with amounts maskable and
    a monthly trend. Aggregation stays in the view, so there is one path
    through the arithmetic rather than a server total and a client total that
    must agree.
- **Mechanical architecture gates** — TDD, DDD and Clean Architecture rules
  enforced by git hooks rather than by memory: no domain literals outside
  `domain/`, no repository interfaces in `domain/`, no cross-module imports,
  gofmt and commit-message shape.
- **Health and readiness probes**, and a multi-arch image published to GHCR on
  release.

### Fixed

Eleven defects of one class — the ledger or the workbook changing without
saying so — found across five independent review rounds. Each is listed
because each was silent: exit code zero, a message that read like success.

- The ledger was replaced with no copy anywhere, while the workbook it syncs
  against had ten. A sync resolving the wrong way took rows with it, and the
  folder holding it is not under version control.
- `CheckLock` recognised LibreOffice's lock file and not Excel's, and read a
  failed `stat` as "not locked" — a check that cannot look is not a check that
  found nothing.
- The id-column collision guard was installed in one writer of two, and the
  command its error advertised (`--migrate-ids`) could move a bank name into
  the id column, producing a duplicate identity that blocks every later sync.
- A number in the id column was accepted as an id. With raw cell values a date
  arrives as its serial (`46218`), and every digit is a valid id character.
- An account absent from the Счета sheet round-tripped as a source, or
  vanished outright.
- An income silently dropped a category, subcategory or place.
- Correcting a row's kind left the original row in place: the same id on two
  sheets, both amounts summing into any selection.
- Backup snapshots taken within one second overwrote each other, and rotation
  scoped by extension deleted a neighbouring file's history.
- `MoneyFromFloat` saturated instead of refusing, so `"Inf"` became 92
  quadrillion rubles; `Money.String` produced `"--92233720368547758.-8"` for
  the most negative amount; `ParseMoney` overflowed on a large parseable
  integer; and an amount whose sign cannot be flipped is now refused.
- `--dry-run` on `fin sync --init` performed the operation in full.
- Saving the workbook dropped its permissions from 0600 to 0755.

### Changed

- `financejsonl.Save` takes a clock, which names the backup it leaves behind.
- `Migration` reports `Rewrote` alongside `Moved`: a header-only migration
  writes the file and moved no rows, and the old report called that "nothing
  to move".
- Dashboard views grew from seven to ten.

### Known limits

Stated rather than left implied:

- `Money.Add` is unchecked int64 addition. The transaction constructor now
  refuses the amounts that could approach a wrap, and the real ledger's totals
  are four orders of magnitude short of one; the upgrade path is a checked add
  returning an error.
- `checkIsID` accepts a latin word made only of id-alphabet letters and absent
  from the Счета sheet. The two classes this book actually holds — Cyrillic
  names and numbers — are both refused.

## [0.1.0] — 2026-06-24

> Initial release: a data-agnostic knowledge-base engine with an embedded
> dashboard, served from a single Go binary.

### Added

- **Domain model** — `Entry` and `Catalog` with typed value objects
  (`Verdict`, `ReadState`, `PublishStage`, `Lifecycle`, `Category`), enforced
  invariants, domain errors, and defensive copying.
- **Catalog adapter** — anti-corruption loader that normalizes the legacy JSON
  shape, plus a faithful append-only writer that preserves existing entries and
  metadata byte-for-byte in content.
- **Audits** — outdated candidates (keyword + skip-unavailable), canonical
  candidates (by reference count), canonical health, supersession integrity,
  and age (Habr articles older than 18 months).
- **Duplicate detection** — exact-URL and normalized-title grouping.
- **Inbox ingestion** — map bot-collected articles into the catalog with
  URL deduplication and id allocation.
- **Task audit** — ADR-015 consistency check between a task list and the
  catalog (orphans / pending-present / consistent).
- **Analytics** — growth-by-week and category sizes, plus an optional curated
  semantic layer (patterns, gaps, contradictions, manifesto quotes).
- **Changelog parser** — Keep-a-Changelog markdown to structured JSON.
- **HTTP API** — `/api/stats`, `/api/entries`, `/api/audits`,
  `/api/duplicates`, `/api/analytics`, `/api/analytics-config`.
- **Dashboard** — embedded React + Vite SPA with seven views (Overview,
  Entries, Analytics, Audits, Duplicates, Archives, Summary), served live by
  the binary via `go:embed`.
- **CLI** — `serve`, `audit`, `dedup`, `inbox`, `audit-tasks`, `changelog`.
- **Tooling** — `just` recipes, golangci-lint v2 config, GitHub Actions CI.
