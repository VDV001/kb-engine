# ADR-0001 — Engine architecture (Go, TDD + DDD + Clean Architecture)

- Status: accepted
- Date: 2026-06-24

## Context

We need a knowledge-base engine that is publicly readable (a third-party
developer should be able to follow it), senior-grade, works with any knowledge
base, and contains no personal data. The catalog is a single JSON file with
heterogeneous, loosely-typed entries; the engine must read it strictly, audit
it, and serve a dashboard.

## Decision

Build the engine in **Go**, following **TDD + DDD + Clean Architecture**.

### Language

Go 1.26. Strong typing maps well onto DDD invariants, the standard library
covers HTTP and JSON without heavy dependencies, and a single static binary
(with the dashboard embedded via `go:embed`) is easy to distribute.

### Layers (Clean Architecture, dependencies point inward)

- `internal/domain` — entities / value objects with invariants. No I/O, no
  infrastructure dependencies. Domain errors as `var ErrXxx`.
- `internal/usecase` — scenarios (audits, stewardship, analytics, report
  assembly). Repository interfaces are declared here (DIP: the interface lives
  with its consumer, not in `domain`).
- `internal/adapter` — implementations: catalog JSON read/write, HTTP API,
  parsers, report rendering.
- `cmd/kbengine` — CLI: parse flags → use case → output. No business logic.

### Discipline (mechanical gates)

- **TDD**: two commits per behaviour — `test(...)` (RED) → `feat(...)` (GREEN).
  A single `feat:` bundling tests is a violation.
- **DDD**: entities / VOs only via `NewXxx(...) (*X, error)` with invariant
  validation; direct `domain.X{...}` outside `domain/` is forbidden; business
  invariants live in `domain`.
- **CA**: handlers / CLI carry no business logic; repository interfaces in
  `usecase`; no cross-boundary coupling. Time is injected, never read from the
  wall clock inside a handler.
- **Verification**: before claiming "senior-grade", an external review must
  score each axis (TDD / DDD / CA) ≥ 8/10. No self-certification.

### Data

The engine is data-agnostic: the knowledge-base path is passed by flag. No
real knowledge base is committed. Tests use synthetic fixtures.

## Consequences

- Test-first discipline keeps a clean, well-covered base and an honest history.
- The strict domain rejects malformed input early, so the anti-corruption layer
  (`adapter/catalogjson`) carries all the messy normalization.
- A single binary with an embedded dashboard ships without a Node toolchain at
  runtime.
