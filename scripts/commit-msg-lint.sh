#!/usr/bin/env bash
# Lightweight, dependency-free Conventional Commits check.
# Usage: commit-msg-lint.sh <path-to-commit-msg-file>
set -euo pipefail

msg_file="${1:?usage: commit-msg-lint.sh <commit-msg-file>}"
header="$(head -n1 "$msg_file")"

# Let git's own machinery (merges, reverts, fixup/squash) through untouched.
if printf '%s' "$header" | grep -qE '^(Merge |Revert |fixup!|squash!)'; then
  exit 0
fi

# <type>(<optional scope>)<optional !>: <subject>
pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9._/-]+\))?!?: .+'

if ! printf '%s' "$header" | grep -qE "$pattern"; then
  {
    echo "✖ commit message is not a valid Conventional Commit:"
    echo "    $header"
    echo
    echo "  expected: <type>(<scope>): <subject>"
    echo "  types:    feat fix docs style refactor perf test build ci chore revert"
    echo "  example:  feat(cli): add version command"
  } >&2
  exit 1
fi
