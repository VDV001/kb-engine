# Changelog

All notable changes to kb-engine are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and the
project adheres to [Semantic Versioning](https://semver.org/). (kb-engine can
parse this file itself: `kbengine changelog --in CHANGELOG.md --out changelog.json`.)

## [Unreleased]

### Added

- **`kbengine set` — правка записей, которых движку не хватало.** Он умел
  добавлять и читать, но не менять: каждое изменение `lifecycle`, тегов или
  связей шло сторонним скриптом, который читал каталог в словарь и писал обратно
  — ровно так поля, которых не знает домен, пропадают молча. Команда меняет
  записи по членам объекта в порядке файла: всё, что домен не моделирует, идёт
  насквозь как raw JSON, а правка либо применяется целиком, либо не пишется
  вовсе — id с опечаткой называется по номеру, и файл остаётся нетронутым.
  `--status` намеренно отсутствует: легаси-поле смешивает вердикт, состояние
  прочтения и стадию публикации, и запись его одной строкой отменила бы
  разбор, который загрузчик делает при чтении.

### Fixed

- **Аудит перестал предлагать работу, которая уже сделана.** `outdated`-кандидаты
  пропускали только записи, уже помеченные `outdated`, но не `dead-end` и не
  `superseded` — тоже принятые решения. На живом каталоге из 52 «кандидатов» 49
  давно лежали в `dead-end`, и правка по такому списку заменила бы конкретное
  состояние более общим. Терминальность теперь спрашивается у домена
  (`Lifecycle.IsTerminal`), и находок стало 5 вместо 54 — **без единой правки
  данных**.

### Added

- **Team draws the flow it describes.** The cards in «как движутся задачи» were
  already edges — every title reads «A → B», and the section's own note says
  tasks go down and statuses come back up — but the reader had to assemble the
  picture in their head from eight cards in file order. Now a card can carry
  `from` / `to` (plus `via` for a step that passes through someone, and `kind`
  for a status going back up), and the section renders participants with arrows
  above the cards. Tiers are computed as distance from the entry point rather
  than card order, so the diagram says something the list did not.
- Links are declared in the file, not parsed out of the title: the same
  participant is written «Отдел» in one card and «отдел» in another, and a parser
  would have drawn two boxes. A card without `from`/`to` stays a plain card.
- **A legend beside the diagram** explains the notation and fills the space a
  narrow diagram left empty. Its counts, entry points and end points are derived
  from the diagram itself — a hand-written list would disagree with the picture
  the first time the file changed.

### Fixed

- **The dashboard went blank when the catalog had no duplicates.** `/api/duplicates`
  answered `null` rather than `[]` — Go writes a nil slice that way — and the tab
  badge took `.length` of it, so React tore down the whole tree. The page that
  broke was Dashboard; the code that broke belonged to Health. Nobody had seen it
  because the catalog always had at least one group to report, and the previous
  release's dedup fix removed the last one. The server now answers with an empty
  list, as its neighbours (entries, finances) already did, and the client
  normalises what it receives besides — an older engine on the other end is not a
  reason for a white screen. Found by opening the page, not by a test: every test
  fixture has duplicates in it.

- **`--from` now says why, instead of failing later or not at all.** Two
  silences, both measured against a running engine rather than assumed. A path
  that could not be read let the server start with its usual line and answer
  every other view; the mistake surfaced only on the Finances tab, as a 500
  reading «finances unavailable» that named neither the flag nor the file. And
  `--from` without `--ledger` was accepted and dropped — `/api/finances` replied
  200 with no balances, and nothing said the workbook had been ignored. Both stop
  at startup with the reason now, which is the contract `--analytics-config` has
  had all along. This was the last flag that took an unusable value in silence.

### Changed

- **The dashboard bundle left the repository.** `frontend/dist` was committed so
  a build needed nothing but Go. The bill came in merges: a generated file has no
  correct side, so two branches touching the UI conflicted on it and the only
  honest resolution was ever «rebuild». It is now an ordinary build artifact, and
  every path that produces a binary builds it first — a Node stage in the image,
  a goreleaser before-hook for releases, one step in each CI job that compiles Go.
- **Building from source now needs Node as well as Go.** That is the price, and
  it is stated where people meet it: README, CONTRIBUTING, and the pre-push gate,
  which checks for the bundle and prints `just web` rather than leaving Go's
  «pattern all:frontend/dist: no matching files found» to be decoded.
- Released binaries and the container image are unaffected — the dashboard is
  built into them exactly as before.

### Removed

- **The «dist matches src» CI gate**, with nothing put in its place. It guarded a
  real hole — an edit to `src` without a rebuild would have shipped the old bundle
  with every Go test still green — but that hole existed only because the bundle
  lived in git. With the file gone the failure cannot happen, so the cause is
  removed instead of watched.

## [0.5.0] — 2026-07-31

> Housekeeping stops being a place the interface abandons you: findings are
> readable, clickable and grouped, and the two views that held them collapse
> into one. The engine also starts saying which build is answering — and admits
> when it did not understand the file it was handed.

### Added

- **Health** — one view for catalog hygiene, replacing the `Audits` and
  `Duplicates` tabs. Neither had enough to fill a top-level tab: on 1340
  entries dedup finds one group and supersession none. Machine reasons are
  translated and grouped, so `verdict:skip-unavailable` becomes one collapsed
  section «Автор снял статью · 48» instead of the same badge printed fifty-one
  times down the page. A finding opens its entry in the archive, and entries
  landing in two audit sections at once are raised to the top: the engine is
  proposing two things that cannot both be done, which is a question rather
  than two pieces of advice.
- **Duplicate groups show entries, not ids.** A group used to render as
  `ids: 1044, 1050` plus a normalised key — enough to know something matched,
  never enough to decide whether it should have. Titles, dates, statuses and
  links now sit side by side. The first thing this showed on live data was that
  the only duplicate in the catalog is a false positive: «Часть 1» and
  «Часть 2» of one article, joined because `normalizeTitle` drops the part
  number.
- **`#481` in the archive search** matches the entry with that id rather than
  the substring — `481` would also match 1481 and any title containing it.
- **`/api/engine`** serves the running build: version, commit and build time.
  They existed only behind `kbengine version` in a terminal, while the footer
  offered sources under AGPL §13 without saying sources of *what*. `kbengine
  version` and the endpoint read the same function and cannot drift.
- **About** replaces `Summary`, which called itself three different things and
  configured none of them. Categories carry their readable names and open in
  the archive on click; an engine card names the version, build, licence and
  links to releases rather than embedding their history in the bundle.

### Fixed

- **An unparsed changelog no longer reports itself as version `0.0.0`.**
  `--changelog` expects `CHANGELOG.md`, while `changelog.json` — the parsed
  form of the same file — sits next to the catalog, so handing over the second
  is an easy mistake, and the markdown parser answered it with an empty
  document. Startup now says it found no releases and, for a `.json` path,
  which file it actually wanted. A warning rather than an error: a young
  project's changelog may legitimately have none yet.
- **Go's pseudo-version is shortened for display** —
  `v0.4.1-0.20260731180902-9f258b58e907+dirty` reads as `v0.4.1-dev+правки`.
  The date and commit inside it repeat the two rows underneath, and the string
  wrapped onto two lines. Irrelevant for a tagged release, unavoidable for
  anyone building from source.
- **Finance export** produces a real `.xlsx` instead of comma-separated CSV,
  which a Russian-locale Excel loaded into a single column; the accounts card
  lists accounts rather than how a row was entered.
- **The analytics sidebar carries all eight blocks**, not three. The data had
  been in the config all along; the `Config` struct truncated it, leaving a
  «9 опор» counter with no supports behind it. An earlier parity audit had
  filed the difference under deliberate deviation without checking it.
- **The AGPL §13 footer is silent on a loopback address.** It cannot be removed
  — §13 covers anyone using the engine over a network — but on `127.0.0.1`
  there is nobody to offer sources to.
- **The image size in the README is measured, not guessed**: 13.5 MB, against
  «~9 MB» and «8.5 МБ» claimed in two different places.

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
