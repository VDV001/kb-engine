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
- **Dashboard** — embedded React + Vite SPA (Overview, Entries, Analytics,
  Audits, Duplicates, Archives, Summary), served live by the binary.

## Quick start

```sh
# Build (needs only Go; the dashboard is embedded via go:embed)
just build            # or: go build -o bin/kbengine ./cmd/kbengine

# Serve the dashboard against a catalog
./bin/kbengine serve --catalog path/to/catalog.json
# → open http://localhost:8080
```

## Commands

```
kbengine serve        --catalog X [--analytics-config Y] [--addr :8080]
kbengine audit        --catalog X [--check outdated|canonical|canonical-health|supersession|age|all]
kbengine dedup        --catalog X
kbengine inbox        --catalog X --inbox DIR [--processed DIR]
kbengine audit-tasks  --catalog X [--json] < tasklist
kbengine changelog    --in CHANGELOG.md --out changelog.json
kbengine version
```

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

The image is a ~9 MB distroless/static binary with the dashboard embedded.
The catalog is runtime data, so mount it and pass `--catalog`:

```sh
# Pull a released multi-arch image from GHCR...
docker run --rm -p 8080:8080 -v "$PWD/data:/data:ro" \
  ghcr.io/vdv001/kb-engine:latest serve --catalog /data/catalog.json

# ...or build it locally:
just docker
docker run --rm -p 8080:8080 -v "$PWD/data:/data:ro" \
  kbengine:dev serve --catalog /data/catalog.json
```

Released tags (`v*`) publish `linux/amd64` + `linux/arm64` images to
`ghcr.io/vdv001/kb-engine` (`latest`, `MAJOR.MINOR`, and the full version).

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

MIT. See [LICENSE](LICENSE).
