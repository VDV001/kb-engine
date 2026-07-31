#!/usr/bin/env bash
#
# Pre-push gates for kb-engine. Everything too slow for pre-commit lands here:
# the full test suite, the race detector, the coverage floor and the linter.
#
# Split rationale: pre-commit must stay fast enough that nobody reaches for
# --no-verify, and a TDD RED commit must be able to land. Push is the last point
# before the work becomes shared, so this is where the hard thresholds live.
#
# Usage: scripts/gates/push.sh          (called by lefthook)
#        SKIP_SLOW=1 scripts/gates/push.sh   (fast gates only — local escape hatch)

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

COVERAGE_FLOOR="${COVERAGE_FLOOR:-80}"

# ------------------------------------------------------- 1. branch guard
# Facet #8 applied to the workflow: publishing to a protected branch is the
# irreversible step, so it is blocked mechanically rather than by remembering.
#
# Decided from what is actually being pushed, not from where HEAD happens to
# sit. git feeds a pre-push hook one line per ref on stdin:
#
#   <local ref> <local sha> <remote ref> <remote sha>
#
# Reading HEAD instead blocked `git push origin v0.2.0` while standing on main —
# a tag writes to refs/tags and touches no branch, so the refusal was about the
# wrong thing and every release tripped over it.
#
# Falls back to the HEAD check when stdin carries no refspec (a direct
# invocation, or a hook runner that does not forward it). Silence must not be
# read as permission: the fallback is what keeps this gate from weakening into
# nothing the day the runner changes.
branch="$(git rev-parse --abbrev-ref HEAD)"
saw_refspec=0
protected_ref=""
while read -r _local_ref _local_sha remote_ref _remote_sha; do
  [ -z "$remote_ref" ] && continue
  saw_refspec=1
  case "$remote_ref" in
    refs/heads/main|refs/heads/master) protected_ref="${remote_ref#refs/heads/}" ;;
  esac
done

if [ "$saw_refspec" -eq 1 ]; then
  if [ -n "$protected_ref" ]; then
    echo "✘ direct push to $protected_ref is blocked — open a PR from a feature branch"
    echo "  (project red line: no pushing straight to main)"
    exit 1
  fi
else
  case "$branch" in
    main|master)
      echo "✘ direct push to $branch is blocked — open a PR from a feature branch"
      echo "  (project red line: no pushing straight to main)"
      exit 1
      ;;
  esac
fi

# ------------------------------------------------------ 2. go.mod is tidy
# A stale go.mod/go.sum breaks CI for everyone else, not for the author.
# go.sum is absent while the module has no external dependencies — handle both.
tidy_backup="$(mktemp -d)"
tidy_files=(go.mod)
[ -f go.sum ] && tidy_files+=(go.sum)
cp "${tidy_files[@]}" "$tidy_backup/"

go mod tidy

tidy_dirty=0
for f in "${tidy_files[@]}"; do
  diff -q "$f" "$tidy_backup/$f" >/dev/null 2>&1 || tidy_dirty=1
done
# tidy may also create go.sum where there was none.
[ ! -f go.sum ] || [ -f "$tidy_backup/go.sum" ] || tidy_dirty=1

if [ "$tidy_dirty" -ne 0 ]; then
  cp "$tidy_backup"/* .
  rm -rf "$tidy_backup"
  echo "✘ go mod tidy produces changes — run 'go mod tidy' and commit the result"
  exit 1
fi
rm -rf "$tidy_backup"

# ---------------------------------------------------------- 3. it builds
# The bundle is a build artifact and is not in the repository, so a fresh clone
# has no frontend/dist and every Go build below fails on the go:embed pattern.
# Go's own message ("pattern all:frontend/dist: no matching files found") does
# not say what to run, so say it here instead of letting people guess.
if [ ! -f frontend/dist/index.html ]; then
  echo "✘ frontend/dist is missing — the dashboard bundle is built, not committed"
  echo "  fix: just web    (or: cd frontend && npm ci && npm run build)"
  exit 1
fi

echo "→ go build ./..."
if ! go build ./... 2>&1; then
  echo "✘ build failed — do not push a tree that does not compile"
  exit 1
fi

# ------------------------------------------- 4. architectural gates (all)
# pre-commit checks only staged files; here the whole tree is verified, so a
# violation cannot slip in through a file that was never staged in this branch.
echo "→ architectural gates (full tree)..."
if ! ./scripts/gates/arch.sh; then
  exit 1
fi

if [ "${SKIP_SLOW:-0}" = "1" ]; then
  echo "⚠ SKIP_SLOW=1 — skipped tests, coverage and lint (CI still gates them)"
  exit 0
fi

# ------------------------------------------------ 5. tests + race detector
echo "→ go test ./... -race"
if ! go test ./... -race -coverprofile=coverage.out -covermode=atomic 2>&1 | tail -20; then
  echo "✘ tests or the race detector failed — fix before pushing"
  exit 1
fi

# ------------------------------------------------------ 6. coverage floor
total="$(go tool cover -func=coverage.out | awk '/^total:/ {print substr($3, 1, length($3)-1)}')"
echo "→ coverage: ${total}% (floor ${COVERAGE_FLOOR}%)"
if awk -v t="$total" -v f="$COVERAGE_FLOOR" 'BEGIN { exit !(t+0 < f+0) }'; then
  echo "✘ coverage ${total}% is below the ${COVERAGE_FLOOR}% floor"
  echo "  Note: coverage measures reach, not correctness — a covered line can still"
  echo "  assert nothing. Raising the number by testing getters defeats the gate."
  exit 1
fi

# ------------------------------------------------------------- 7. linter
# Blocking here (unlike pre-commit): by push time there is no legitimate
# half-written state left.
if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint (incl. modernize)..."
  if ! golangci-lint run ./... 2>&1; then
    echo "✘ golangci-lint found issues — fix before pushing"
    exit 1
  fi
else
  echo "⚠ golangci-lint not installed — skipping (install: brew install golangci-lint)"
fi

# ------------------------------------------------- 8. secrets in the range
if command -v gitleaks >/dev/null 2>&1; then
  echo "→ gitleaks (commits being pushed)..."
  if ! gitleaks git --no-banner --redact --exit-code 1 . 2>&1; then
    echo "✘ gitleaks found a secret — do not push"
    exit 1
  fi
else
  echo "⚠ gitleaks not installed — skipping (install: brew install gitleaks)"
fi

echo "✓ pre-push gates passed"
