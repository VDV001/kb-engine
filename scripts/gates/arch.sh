#!/usr/bin/env bash
#
# Architectural gates for kb-engine. Grep-level checks that catch the DDD /
# Clean Architecture violations which a linter won't see.
#
# These are BLOCKING on purpose. Unlike lint findings, none of them is ever a
# legitimate intermediate state: a domain type built outside its package skips
# invariant validation, and a domain package that imports an adapter has already
# inverted the dependency rule. There is no commit where those are "fine for now".
#
# Usage: scripts/gates/arch.sh [file.go ...]
#        With no arguments, checks every tracked .go file.
#
# Manifesto facets these enforce (honest mapping — not every facet is mechanizable):
#   #8 Applicability — the deterministic boundary is held by code, not by intent:
#      the domain stays free of time/rand/uuid so its decisions are reproducible.
#   #7 Dissociation  — the rule lives in infrastructure, not in a prompt or a
#      reviewer's memory, so it cannot quietly stop being applied.

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Collect targets: explicit args, or every tracked Go file.
files=()
if [ "$#" -gt 0 ]; then
  for f in "$@"; do
    case "$f" in *.go) [ -f "$f" ] && files+=("$f") ;; esac
  done
else
  while IFS= read -r line; do
    [ -n "$line" ] && files+=("$line")
  done < <(git ls-files '*.go')
fi

[ ${#files[@]} -eq 0 ] && exit 0

fail=0

# code_of <file> — strips line comments before matching, so a comment that
# *mentions* a banned construct (explaining why it is avoided) does not trip the
# gate. Line numbers are preserved so reports stay accurate.
#
# ponytail: sed, not a Go AST parser. Ceiling — a // sequence inside a string
# literal (a URL) loses the tail of that line for matching purposes; none of the
# five gates matches on URLs, so the blind spot is inert. Upgrade path if that
# ever bites: a tiny go/ast-based checker invoked from here.
code_of() {
  sed 's|//.*||' "$1"
}

# report <gate> <file> <hits>
report() {
  echo "✘ $1"
  printf '%s\n' "$3" | sed "s|^|    $2:|"
  fail=1
}

for f in "${files[@]}"; do
  case "$f" in *_test.go) is_test=1 ;; *) is_test=0 ;; esac
  case "$f" in internal/domain/*) in_domain=1 ;; *) in_domain=0 ;; esac

  # ---------------------------------------------------------------- gate 1
  # &domain.X{...} outside the domain package bypasses NewXxx(...) and its
  # invariant validation. Inside the package it is legitimate.
  if [ "$in_domain" -eq 0 ]; then
    if hits="$(code_of "$f" | grep -nE '&domain\.[A-Z][A-Za-z0-9]*\{' || true)" && [ -n "$hits" ]; then
      report "DDD: domain struct built outside domain/ — use NewXxx(...)" "$f" "$hits"
    fi
  fi

  # ---------------------------------------------------------------- gate 2
  # Repository/Store ports belong to the consumer (usecase/), not the domain.
  # DIP by the book: the inner layer declares nothing about persistence.
  if [ "$in_domain" -eq 1 ] && [ "$is_test" -eq 0 ]; then
    if hits="$(code_of "$f" | grep -nE 'type +[A-Za-z0-9]*(Repository|Repo|Store) +interface' || true)" && [ -n "$hits" ]; then
      report "CA/DIP: persistence port declared in domain/ — move it to usecase/" "$f" "$hits"
    fi
  fi

  # ---------------------------------------------------------------- gate 3
  # The dependency rule: domain imports nothing from the outer layers.
  if [ "$in_domain" -eq 1 ]; then
    if hits="$(code_of "$f" | grep -nE '"github\.com/daniil/kb-engine/internal/(adapter|usecase)' || true)" && [ -n "$hits" ]; then
      report "CA: domain/ imports an outer layer — the dependency rule points inward only" "$f" "$hits"
    fi
  fi

  # ---------------------------------------------------------------- gate 4
  # Non-determinism in the domain (facet #8). Clock and randomness are inputs,
  # passed in by the caller — otherwise the same entry yields different results
  # on two runs and nothing about it is reproducible or testable.
  if [ "$in_domain" -eq 1 ] && [ "$is_test" -eq 0 ]; then
    if hits="$(code_of "$f" | grep -nE '\b(time\.Now|rand\.|uuid\.New)' || true)" && [ -n "$hits" ]; then
      report "Facet #8: non-determinism in domain/ — pass the clock/id in as a parameter" "$f" "$hits"
    fi
  fi

  # ---------------------------------------------------------------- gate 5
  # Debug prints in library code. cmd/ prints legitimately; internal/ returns
  # errors and logs through the injected logger instead.
  case "$f" in
    internal/*)
      if [ "$is_test" -eq 0 ]; then
        if hits="$(code_of "$f" | grep -nE '\bfmt\.Print(ln|f)?\(' || true)" && [ -n "$hits" ]; then
          report "hygiene: fmt.Print* in internal/ — return an error or use the logger" "$f" "$hits"
        fi
      fi
      ;;
  esac
done

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "  Architectural gates failed. These are mechanical and blocking by design:"
  echo "  a rule that can be skipped when inconvenient is not a rule."
  exit 1
fi

echo "✓ architectural gates passed (${#files[@]} file(s))"
