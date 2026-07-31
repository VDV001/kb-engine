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
  formatting.
- **Frontend bundle**: `frontend/dist` is a build artifact and is **not**
  committed — run `just web` once after cloning, and again after any change under
  `frontend/`. Everything that compiles Go reads it from disk through `go:embed`,
  so without it even `just test` fails; the pre-push gate says what to run. It
  used to be committed, and every second branch touching the UI then conflicted
  on generated files that have no correct side in a merge.

## Local checks

```sh
just web         # build the dashboard bundle (first clone / after frontend/ changes)
just test        # unit tests
just test-race   # race detector
just lint        # golangci-lint
just cover       # coverage summary
just ci          # everything CI runs
```
