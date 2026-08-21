## What & why

<!-- What does this PR change, and why?

Link issues with an ENGLISH keyword: "Closes #123" / "Fixes #123". GitHub does
not understand other languages, and a Russian "Закрывает #123" leaves the issue
open with nobody noticing. -->

## How verified

<!-- Commands run and their result. -->
- [ ] `just ci` passes (lint + race tests + coverage)
- [ ] New behaviour covered by tests (TDD: failing test first)

## Checklist

- [ ] Follows the layering in [docs/adr/0001-architecture.md](../docs/adr/0001-architecture.md)
- [ ] No business logic in handlers/CLI; invariants live in `domain`
- [ ] `CHANGELOG.md` updated under `[Unreleased]` if user-facing

## If this PR is stalled

<!-- Delete this section unless it applies. A stalled PR without a stated reason
and date is indistinguishable from one nobody looked at yet. -->

- [ ] Labelled `blocked` or `deferred`
- [ ] Comment says: why, what clears it, and the date to revisit
