# kb-engine

Data-agnostic knowledge-base engine in Go. It loads a catalog of entries
(articles, notes, creations), runs lifecycle and stewardship audits, finds
duplicates, ingests new entries, computes analytics, and serves an embedded
React dashboard — all from a single static binary.

The engine is data-agnostic: the catalog path is passed by flag, so it works
with any knowledge base. No personal data lives in this repository.

Built with TDD + DDD + Clean Architecture. Design notes:
[docs/adr/0001-architecture.md](docs/adr/0001-architecture.md).

## Features

- **Catalog model** — entries with typed value objects (verdict, read-state,
  publish-stage, lifecycle, category) and enforced invariants.
- **Audits** — outdated candidates (keyword + skip-unavailable), canonical
  candidates (by reference count), canonical health, supersession integrity,
  age (Habr articles older than 18 months).
- **Duplicate detection** — exact-URL and normalized-title grouping.
- **Inbox ingestion** — map bot-collected articles into the catalog with
  deduplication and a faithful, non-lossy writer.
- **Analytics** — growth over time, category sizes, plus an optional curated
  semantic layer (patterns / gaps / contradictions / manifesto).
- **Changelog** — parse a Keep-a-Changelog `CHANGELOG.md` into structured JSON.
- **Dashboard** — embedded React + Vite SPA (Dashboard, Archives, Analytics,
  Projects, Now, Team, Finances, Health, About), served live by the binary.
  `Audits` and `Duplicates` merged into `Health` in 0.5.0; `Summary` became
  `About`.
- **Entry editing** — `set` changes lifecycle, verdict, tags, versions and links
  in place, keeping fields the domain does not model untouched.
- **Own artefacts** — `add` puts a standard, a write-up or a draft into the
  catalog. Such an entry has no address on the internet: it is identified by the
  file it lives in, and that file is what the dedup keys on.
- **Terminal UI** — `tui` opens the same catalog in the terminal: type to filter,
  arrows to move, Enter for the entry card. With `--ledger` it also opens the
  finances screen on `Tab`, where `a` records an expense and `i` an income
  through the same use case `fin add` writes with, and `q` takes the whole entry
  as one line. Add `--from` and `s` carries the rows over to the workbook — the
  same sync `fin sync` runs — while `b` shows what each account holds and records
  a new balance, the same write `fin balance` performs. It reads through the same
  use case the dashboard does, so the two surfaces cannot disagree.
- **Link drift** — `drift` checks whether the catalog's urls are still alive,
  records the verdict, and can replace an address with the canonical one from a
  redirect. Its report starts with what it did **not** check.

## Status

Каждая возможность помечена честным статусом. Источник таблицы один — срез
`Capabilities()` в движке; та же таблица на вкладке About (`/api/capabilities`),
а гейт `scripts/gates/capabilities.sh` не даёт README разойтись с источником.

| Возможность | Статус | Замечание |
|---|---|---|
| Каталог знаний | ✅ stable | Записи, категории, статусы жизненного цикла; аудит целостности. |
| Поиск: подстрока, транслитерация, опечатки, словарь | ✅ stable | Четыре слоя в одном usecase; терминал и веб отвечают одинаково. |
| Смысловой слой поиска | ⚠️ experimental | Векторы от внешней службы; закрывает единичные запросы, не все. |
| Финансовый учёт | ✅ stable | Журнал + xlsx в шаге; балансы, отчёты, проверка написаний. |
| Терминальный интерфейс (TUI) | ✅ stable | Второй равноправный интерфейс поверх того же usecase. |
| Веб-витрина | ✅ stable | Дашборд, карты архитектуры, артефакты базы через /kb/. |
| Метрики Prometheus | ✅ stable | Формат текста без клиентской библиотеки; /metrics. |
| Профилировщик pprof | ✅ stable | Под флагом, на своём слушателе; выключен по умолчанию. |
| Разбивка ответа (Server-Timing) | ✅ stable | Шаги запроса в заголовке; выключено по умолчанию. |
| Развёртывание в Kubernetes и Helm | ⚠️ experimental | Манифесты и чарт написаны; нагрузка и многоузловость не проверены. |
| Хранилище каталога в S3 (Terraform) | ⚠️ experimental | Проверено на LocalStack и MinIO; настоящий AWS не прогонялся. |
| MCP-сервер над каталогом | ⚠️ experimental | Отдельный бинарь kbengine-mcp; подключён в Claude Code и здоровается, инструменты из агента ещё не звались. |

## Quick start

Released binaries and the container image are self-contained — the dashboard is
already inside them. Building **from source** needs Node as well as Go: the
bundle is a build artifact, so it is not kept in the repository.

```sh
# Build the dashboard bundle (needs Node; repeat after frontend/ changes)
just web

# Build the binary — go:embed folds the bundle into it
just build            # or: go build -o bin/kbengine ./cmd/kbengine

# Serve the dashboard against a catalog
./bin/kbengine serve --catalog path/to/catalog.json
# → open http://localhost:8080
```

## Commands

```
kbengine serve        --catalog X [--analytics-config Y] [--ledger L --from W] [--changelog C]
                      [--now N] [--team T] [--projects P] [--media DIR] [--addr HOST:PORT]
kbengine add          --catalog X --title T --category C --file PATH [--description D] [--tags T]
                      [--version SEMVER] [--lifecycle L] [--source S]
kbengine tui          --catalog X [--ledger L] [--from WORKBOOK]
kbengine audit        --catalog X [--check outdated|canonical|canonical-health|supersession|integrity|versions|batch|links|age|all]
kbengine dedup        --catalog X
kbengine drift        --catalog X [--apply] [--update-urls] [--limit N] [--delay D] [--timeout T]
kbengine migrate      <versions|urls> --catalog X [--apply]
kbengine set          --catalog X --ids 1,2,3 [--lifecycle V] [--verdict V] [--add-tag T] [--remove-tag T]
                      [--related IDS] [--version SEMVER | --revision N] [--file PATH] [--url ADDR]
                      [--notes N] [--author A] [--title T] [--description D] [--supersedes ID]
kbengine inbox        --catalog X --inbox DIR [--processed DIR]
kbengine audit-tasks  --catalog X [--json] < tasklist
kbengine changelog    --in CHANGELOG.md --out changelog.json
kbengine fin          <import|add|balance|list|report|sync> [flags]
kbengine version
```

`set` is the only command that edits existing entries. It rewrites them member by
member in the order the file already has, so fields the domain does not model are
carried through untouched, and it applies either every id or none — a typo in one
id of fifty leaves the catalog exactly as it was:

```sh
kbengine set --catalog X --ids 809,1087 --lifecycle outdated
kbengine set --catalog X --ids 42 --add-tag go,harness --remove-tag old
kbengine set --catalog X --ids 42 --related 469,478      # --related= clears the list
kbengine set --catalog X --ids 42 --file notes/2026-08-01_x.md --url=   # move a path out of url
```

`--title`, `--description` and `--supersedes` describe a single entry and refuse
several ids: a title written to fifty entries at once is not an edit anyone
meant. `--notes` and `--author` may be set on a group.

An empty string means "the flag was not passed", never "clear the field": a
forgotten flag must not wipe data. Clearing is a separate instruction — `--url=`
and `--related=` — and it conflicts with setting the same field.

`--check links` is deliberately **not** part of `--check all`. With hundreds of
entries never checked, its output would bury the handful of real findings; `all`
prints a one-line summary and names the command instead.

`--check integrity` reports links that point nowhere: a `related_ids` value with
no such entry, or one pointing at itself. Neither shows up on any screen — a
broken link simply does not draw.

### MCP server (`kbengine-mcp`)

A separate binary that serves the catalog to coding agents over MCP (stdio).
Three tools — `search_catalog`, `get_entry`, `stats` — call the *same* usecase
the dashboard and the TUI call, so an agent and a human cannot get different
answers to one question.

```jsonc
// ~/.claude.json or another MCP client config
{
  "mcpServers": {
    "kb": {
      "command": "kbengine-mcp",
      "args": ["--catalog", "/path/to/catalog.json"]
    }
  }
}
```

It ships in the same release archives as `kbengine`: apart they would drift into
different versions, and "search answers differently over here" would become a
question with no answer.

Two deliberate limits, both measured rather than assumed. The MCP SDK adds
~4 MB, so this is a separate binary — `kbengine` itself is byte-for-byte
unchanged by it, and a stdio server is pointless inside the dashboard image.
What goes out is exactly what the catalog publishes: the same allow-list as the
`/kb/` route, so personal notes and the finance workbook — which live in the
same tree but are not named by any entry — cannot appear in a response.

## Stack

Go 1.26 · standard library · React + Vite + Tailwind · golangci-lint v2 · just ·
GitHub Actions CI.

## Development

```sh
just             # list recipes
just test        # unit tests
just test-race   # with the race detector
just lint        # golangci-lint
just cover       # coverage summary
just cover-gate  # fail if total coverage drops below 80%
just hooks       # install git hooks (gitleaks + Conventional Commits)
just web         # rebuild the embedded frontend after UI changes
just docker      # build the container image (kbengine:dev)
just ci          # full gate (tidy + lint + race tests + coverage gate)
```

### Docker

The image is a ~17 MB distroless/static binary with the dashboard embedded.
(Measured, not estimated: `just docker` then `docker images kbengine:dev`. It grew
with the dashboard — the figure was ~9 MB while the SPA was a fraction of its
current size.)
The catalog is runtime data, so mount it and pass `--catalog`:

```sh
# Pull a released multi-arch image from GHCR...
docker run --rm -p 8080:8080 -v "$PWD/data:/data:ro" \
  ghcr.io/vdv001/kb-engine:latest serve --catalog /data/catalog.json --addr :8080

# ...or build it locally:
just docker
docker run --rm -p 8080:8080 -v "$PWD/data:/data:ro" \
  kbengine:dev serve --catalog /data/catalog.json --addr :8080
```

Released tags (`v*`) publish `linux/amd64` + `linux/arm64` images to
`ghcr.io/vdv001/kb-engine` (`latest`, `MAJOR.MINOR`, and the full version).

The server exposes `GET /healthz` (liveness, always 200) and `GET /readyz`
(readiness, 503 until the catalog loads) for container/orchestrator probes.

Hooks require [`lefthook`](https://github.com/evilmartians/lefthook) and
[`gitleaks`](https://github.com/gitleaks/gitleaks) on `PATH`. CI also runs
gitleaks and enforces the coverage gate independently.

## Architecture (Clean Architecture)

```
cmd/kbengine        CLI: flags → use case → output (no business logic)
internal/domain     entities / value objects with invariants (no I/O)
internal/usecase    scenarios + repository interfaces (DIP)
internal/adapter    catalog JSON, HTTP API, parsers, report rendering
frontend/           React dashboard, embedded into the binary via go:embed
```

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE).

The engine is free to run, modify and self-host. AGPL adds one condition that
matters here: if you run a modified version as a network service, the people
using that service are entitled to its source. For a knowledge base that is the
point — improvements to it should reach the people whose knowledge it holds.

A commercial licence is available for organisations that cannot accept those
terms — see [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md).

Releases up to and including `v0.3.0` were published under MIT. That grant is
irrevocable for those versions: anyone who obtained them under MIT keeps MIT
terms for that code. The change applies from `v0.4.0` onward.
