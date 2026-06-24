# Contributing

Thanks for your interest in kb-engine.

## Workflow

1. Branch from `main` (`feat/...`, `fix/...`, `chore/...`).
2. Develop test-first (TDD): a failing `test(...)` commit, then a `feat(...)` /
   `fix(...)` commit that makes it pass.
3. Run the full gate before pushing: `just ci`.
4. Open a pull request. CI must be green and the PR reviewed before merge;
   history on `main` is linear (squash or rebase).

## Conventions

- **Commits**: Conventional Commits (`feat:`, `fix:`, `test:`, `refactor:`,
  `docs:`, `chore:`).
- **Architecture**: Clean Architecture + DDD — see
  [docs/adr/0001-architecture.md](docs/adr/0001-architecture.md). Business
  invariants live in `internal/domain`; handlers and the CLI carry no business
  logic; repository interfaces live with their consumer (`internal/usecase`).
- **Tooling**: `just` for tasks, `golangci-lint` for linting, `gofmt` for
  formatting. Keep the dashboard in sync with `just web` (the built
  `frontend/dist` is committed for `go:embed`).

## Local checks

```sh
just test        # unit tests
just test-race   # race detector
just lint        # golangci-lint
just cover       # coverage summary
just ci          # everything CI runs
```
