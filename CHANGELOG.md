# Changelog

All notable changes to kb-engine are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and the
project adheres to [Semantic Versioning](https://semver.org/). (kb-engine can
parse this file itself: `kbengine changelog --in CHANGELOG.md --out changelog.json`.)

## [Unreleased]

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
