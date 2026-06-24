## What & why

<!-- What does this PR change, and why? Link issues with "Closes #123". -->

## How verified

<!-- Commands run and their result. -->
- [ ] `just ci` passes (lint + race tests + coverage)
- [ ] New behaviour covered by tests (TDD: failing test first)

## Checklist

- [ ] Follows the layering in [docs/adr/0001-architecture.md](../docs/adr/0001-architecture.md)
- [ ] No business logic in handlers/CLI; invariants live in `domain`
- [ ] `CHANGELOG.md` updated under `[Unreleased]` if user-facing
