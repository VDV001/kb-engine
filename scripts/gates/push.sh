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

# ------------------------------------------- 5. owner's data must not leave
# The repository is public. Amounts, balances and transaction ids out of the
# owner's ledger have no business in code, in the journal or in a commit
# message. The rule was written down in May and broken anyway — by hand, while
# describing the work with real numbers — so it stops being a rule and becomes
# a check.
#
# The markers are read from the live ledger at push time and never stored here:
# a list of someone's balances committed into a public gate would be the very
# leak it guards against.
#
# What it cannot see, said out loud rather than left to be assumed: values that
# no longer appear in the current ledger (an old balance), amounts small enough
# to be indistinguishable from a fixture, and anything already pushed — history
# is not rewritten from here.
if [ "${DATA_SKIP:-0}" != "1" ]; then
  # Its own base. The journal gate above defines one inside its own block, and
  # with CHANGELOG_SKIP=1 that block never runs — this check would then compare
  # against an empty string and pass everything without saying so.
  data_base="origin/main"
  ledger="${KB_LEDGER:-$HOME/claude-cowork/finances/transactions.jsonl}"
  if [ ! -f "$ledger" ]; then
    echo "⚠ ledger not found at $ledger — owner-data check skipped (set KB_LEDGER)"
  elif git rev-parse --verify --quiet "$data_base" >/dev/null; then
    marks="$(mktemp)"
    # Amounts from a thousand up that are not round thousands, plus every
    # transaction id and both totals.
    #
    # Both cuts exist because the first version cried wolf: below a thousand a
    # number is indistinguishable from a fixture, and a round 1000.00 in a test
    # matched a real expense of the same size. A gate that fires on ordinary
    # test data is one somebody switches off within a week.
    python3 - "$ledger" > "$marks" <<'PY'
import json, sys
from decimal import Decimal
out, total = set(), {"expense": Decimal(0), "income": Decimal(0)}
for line in open(sys.argv[1], encoding="utf-8"):
    r = json.loads(line)
    a = Decimal(r["amount"])
    total[r["kind"]] = total.get(r["kind"], Decimal(0)) + a
    if a >= 1000 and a % 1000 != 0:
        out.add(f"{a:.2f}")
    out.add(r["id"])
for v in total.values():
    out.add(f"{v:.2f}")
out.add(f"{total['income'] - total['expense']:.2f}")
import re
# An amount leaks in the shape a human reads, not the shape it is stored in.
# The engine itself prints money with grouped digits, so every marker is
# expanded into all the shapes the same number can take: either decimal
# separator, grouped or not, with a plain space or a non-breaking one, and with
# the sign dropped (prose says "expenses are ...", never "expenses are -...").
#
# Each of those variations was a hole. The first version compared stored
# strings only and let every total through. The second grouped the digits but
# kept the original separator, so it looked for a dot where the leak carries a
# comma. And both totals are negative in the ledger, so a pattern anchored at a
# digit never matched them at all. All three were found by planting a real
# total and watching the gate stay silent - not by reading the code.
forms = set()
for m in (x for x in out if x and x != "0.00"):
    forms.add(m)
    mm = re.fullmatch(r"-?(\d+)[.,](\d{2})", m)
    if not mm:
        continue  # a transaction id: no numeric shapes to expand
    whole, frac = mm.groups()
    groupings = [whole]
    if len(whole) > 3:
        parts = [whole[max(0, k - 3):k] for k in range(len(whole), 0, -3)][::-1]
        groupings += ["\u0020".join(parts), "\u00a0".join(parts)]
    for g in groupings:
        for sep in (".", ","):
            forms.add(f"{g}{sep}{frac}")
for m in sorted(forms):
    print(m)
PY
    hits="$(git diff --unified=0 "$data_base...HEAD" 2>/dev/null | grep '^+' | grep -F -f "$marks" | head -5 || true)"
    msgs="$(git log "$data_base..HEAD" --format=%B 2>/dev/null | grep -F -f "$marks" | head -5 || true)"
    rm -f "$marks"
    if [ -n "$hits$msgs" ]; then
      echo "✘ в изменениях есть числа из живого ledger — репозиторий публичный"
      [ -n "$hits" ] && { echo "  в коде:"; echo "$hits" | sed 's/^/    /'; }
      [ -n "$msgs" ] && { echo "  в сообщениях коммитов:"; echo "$msgs" | sed 's/^/    /'; }
      echo "  Замените выдуманными значениями. Осознанно: DATA_SKIP=1 git push"
      exit 1
    fi

    # ---- Второй класс маркеров: не суммы, а СТРУКТУРА личных финансов.
    #
    # Заведён 06.08.2026 после третьего случая утечки за неделю. Первые два
    # класса (суммы и id) к тому моменту уже ловились, и гейт честно молчал —
    # утекло то, чего он не умеет видеть: имена счетов владельца и счётчики
    # строк его книги. «Займ → Коллеге» не сумма и не id, но говорит о человеке
    # больше, чем любая из них.
    #
    # Имена берутся из ledger, потому что это единственный машиночитаемый
    # источник, который здесь уже открыт. Бренды банков пропускаем намеренно:
    # правило владельца разрешает их в фикстурах, и гейт, ругающийся на слово
    # «Сбербанк» в тесте, будет выключен за неделю. Отбираем счета с родом —
    # стрелка в имени и есть личная структура.
    #
    # ⚠️ ЧЕГО ЭТОТ ГЕЙТ НЕ ВИДИТ, сказано вслух, а не оставлено додумывать:
    #   • Счёт, по которому нет ни одной транзакции, в ledger не попадает вовсе —
    #     а именно так устроен долговой счёт (выдача меняет баланс, а не журнал).
    #     То есть самое чувствительное имя этот источник как раз и пропускает.
    #     Закрывается командой «перечисли счета» у движка; её сегодня нет.
    #   • Счётчики строк ищутся ТОЛЬКО в прозе (*.md) и только рядом со словом
    #     о строках. Замер 06.08 показал, почему шире нельзя: числа 22 и 77
    #     живут в package-lock.json, в SVG и в хешах go.sum, и гейт на голых
    #     числах кричал бы волком в каждом push.
    #   • Уже запушенное. История отсюда не переписывается.
    struct_marks="$(mktemp)"
    python3 - "$ledger" > "$struct_marks" <<'PY'
import json, sys
accounts, counts = set(), {}
rows = [json.loads(l) for l in open(sys.argv[1], encoding="utf-8") if l.strip()]
for r in rows:
    a = (r.get("account") or "").strip()
    if "→" in a:          # род → элемент: личная структура, не бренд
        accounts.add(a)
    counts[r["kind"]] = counts.get(r["kind"], 0) + 1
for a in sorted(accounts):
    print("ACC\t" + a)
sizes = {len(rows), *counts.values()}
for c in sorted(x for x in sizes if x >= 20):   # ниже двадцати неотличимо от фикстуры
    print("CNT\t" + str(c))
PY
    acc_pat="$(awk -F'\t' '$1=="ACC"{print $2}' "$struct_marks")"
    cnt_pat="$(awk -F'\t' '$1=="CNT"{print $2}' "$struct_marks")"
    rm -f "$struct_marks"

    struct_hits=""
    if [ -n "$acc_pat" ]; then
      struct_hits="$(git diff --unified=0 "$data_base...HEAD" 2>/dev/null | grep '^+' \
        | grep -F -f <(echo "$acc_pat") | head -5 || true)"
    fi
    # Счётчики — только в прозе и только рядом со словом о строках/записях.
    prose_hits=""
    if [ -n "$cnt_pat" ]; then
      nums="$(echo "$cnt_pat" | paste -sd'|' -)"
      word='строк|строки|строках|записей|операций'
      md_files="$(git diff --name-only "$data_base...HEAD" -- '*.md' 2>/dev/null || true)"
      [ -n "$md_files" ] && prose_hits="$(grep -nE \
        "($word)[^0-9]{0,20}($nums)([^0-9]|\$)|($nums)[^0-9]{0,20}($word)" \
        $md_files 2>/dev/null | head -5 || true)"
    fi

    if [ -n "$struct_hits$prose_hits" ]; then
      echo "✘ в изменениях есть СТРУКТУРА личных финансов — репозиторий публичный"
      [ -n "$struct_hits" ] && { echo "  имена счетов владельца:"; echo "$struct_hits" | sed 's/^/    /'; }
      [ -n "$prose_hits" ] && { echo "  объёмы личной книги в прозе:"; echo "$prose_hits" | sed 's/^/    /'; }
      echo "  Имена счетов в фикстурах — выдуманные (род → элемент сохранить)."
      echo "  Объёмы книги в CHANGELOG не нужны вовсе: пишите качественно, а не числом."
      echo "  Осознанно: DATA_SKIP=1 git push"
      exit 1
    fi
  fi

  # ---- 5c. Правило про КЛАСС, а не про совпадение со списком.
  #
  # Всё выше сравнивает изменения со СПИСКОМ значений из живого ledger. У такого
  # критерия два врождённых отказа, и оба уже случались:
  #
  #   1. Нет ledger — нет списка — проверка пропускается целиком. На CI ledger'а
  #      нет никогда, значит там этот класс не проверялся ни разу, а строка
  #      "owner-data check skipped" читалась как «чисто». Security default обязан
  #      быть deny, а был allow.
  #   2. Производные не перечислимы. Список знает суммы строк и три итога;
  #      подытог по счёту, средний чек, число дней подряд — каждый новый агрегат
  #      это новый факт вне списка. Утечка 06.08.2026 прошла ровно так.
  #
  # Поэтому здесь запрещена ФОРМА, а не значение: денежный литерал в журнале
  # рядом со словом об остатке, счёте или итоге. Работает без ledger — то есть
  # и на CI — и ловит производные, которых в списке нет по определению.
  #
  # Чего не видит и это правило, названо, чтобы не додумывали: суммы без копеек,
  # числа словами, скриншоты и вложения, и всё уже запушенное.
  money_hits=""
  changelog_diff="$(git diff --unified=0 "origin/main...HEAD" -- CHANGELOG.md 2>/dev/null | grep '^+' || true)"
  if [ -n "$changelog_diff" ]; then
    money_hits="$(printf '%s\n' "$changelog_diff" \
      | grep -nE '(остат|баланс|счёт|счет|итог|потрачено|получено)[^0-9]{0,40}[0-9]{3,}[.,][0-9]{2}|[0-9]{3,}[.,][0-9]{2}[^0-9]{0,40}(остат|баланс|итог|на счёте|на счете)' \
      | head -5 || true)"
  fi
  if [ -n "$money_hits" ]; then
    echo "✘ денежный литерал в CHANGELOG рядом со словом об остатке или итоге"
    echo "$money_hits" | sed 's/^/    /'
    echo "  Журнал описывает, ЧТО чинилось, а не сколько денег у владельца."
    echo "  Если это выдуманное число в примере — уберите копейки или округлите."
    echo "  Осознанно: DATA_SKIP=1 git push"
    exit 1
  fi
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

# ------------------------------------------- 9. behaviour reaches the journal
# A branch that changes behaviour has to say so in CHANGELOG.md.
#
# This gate exists because the same miss happened twice in two days: the
# one-line quick entry, then the health screen and the repeat guard — none of
# them reached the journal on the way in. Both times it was caught by hand,
# comparing Unreleased against the git history right before cutting a version,
# which works exactly as long as somebody remembers to compare.
#
# The entry is written on the branch that makes the change, while what changed
# is still fresh, rather than reconstructed at release time from commit
# subjects.
#
# Признак с 19.08.2026 — состав диффа, а не префикс заголовка: множество
# {feat, fix} расходилось с «стоит записать» в ОБЕ стороны (issue #228).
# Решает scripts/gates/changelog-scope.sh, у него есть --self-test на
# настоящих коммитах истории.
#
# Лазейка остаётся названной, но нужна теперь редко: молчаливые пути (тесты,
# документация, зависимости) гейт пропускает сам. Настоящий оставшийся случай —
# код, у которого ещё нет ни одного вызывающего: диффом он неотличим от
# работающего, и CHANGELOG_SKIP=1 для него честнее, чем запись о том, чего
# пользователь не увидит.
if [ "${CHANGELOG_SKIP:-0}" != "1" ]; then
  base="origin/main"
  if git rev-parse --verify --quiet "$base" >/dev/null; then
    # Признак — СОСТАВ ДИФФА, а не префикс заголовка (issue #228). Решение
    # вынесено в отдельный скрипт, потому что у него есть --self-test на
    # настоящих коммитах истории: правило внутри монолитного pre-push никто
    # прогнать не может, а непрогоняемая проверка проверяет себя сама.
    culprit="$("$(dirname "$0")/changelog-scope.sh" "$base..HEAD" || true)"
    # Counted as commits touching the file, not as a diff against the base, and
    # release commits do not count.
    #
    # Both parts are measured, not assumed. A branch cut from a release branch
    # carries that release's changelog commit along, and a plain diff then shows
    # CHANGELOG.md as "touched" — the health screen branch passed this gate that
    # way while carrying no entry of its own.
    touched="$(git log "$base..HEAD" --format=%s -- CHANGELOG.md | grep -cvE '^docs\(changelog\): релиз' || true)"
    if [ -n "$culprit" ] && [ "$touched" -eq 0 ]; then
      echo "✘ ветка меняет поведение (тронут $culprit), но CHANGELOG.md не тронут"
      echo "  Допишите абзац в раздел [Unreleased] здесь, на этой ветке,"
      echo "  пока помните, что именно изменилось."
      echo "  Если поведение не менялось: CHANGELOG_SKIP=1 git push"
      exit 1
    fi
  fi
fi

echo "✓ pre-push gates passed"
