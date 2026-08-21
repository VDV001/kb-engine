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

## Blocked and deferred work

An issue nobody can start, and an issue nobody has started yet, look identical
from the outside. Label the difference so the tracker keeps meaning what it says:

- **`blocked`** — the work cannot begin. Say what blocks it and what would
  unblock it.
- **`deferred`** — the work was postponed on purpose. Say why, and name the date
  or the condition that brings it back.

Both labels require a comment in this shape, so that the reason survives the
person who knew it:

```
⏸ DEFERRED — decided <date>
Why: …
What clears it: …
Revisit no earlier than: YYYY-MM-DD
```

A pull request that stalls carries the same label and the same comment. A label
without a stated reason and date rots into "someone will remember" — which is
the failure this project keeps finding in its own automation.

Work we decided **not** to do is neither of these: close it with reason
`not planned`, keep the rationale in the closing comment, and say what would
change the decision. Closed issues stay searchable; the open list stays a list
of work.

## Local checks

```sh
just web         # build the dashboard bundle (first clone / after frontend/ changes)
just test        # unit tests
just test-race   # race detector
just lint        # golangci-lint
just cover       # coverage summary
just ci          # everything CI runs
```
