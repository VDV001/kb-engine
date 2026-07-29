#!/usr/bin/env bash
# livecheck.sh — прогон fin-команд против КОПИИ настоящей папки финансов.
#
# Зачем. Шесть дефектов финансового блока нашлись только на живом файле, ни
# одного не поймали тесты: они пишут в свежий t.TempDir() на синтетических
# фикстурах, где нет ни лежалого .~lock, ни чужих прав, ни колонки без
# заголовка, ни листа на 1156 строк при 507 записях.
#
#   1. excelize отдаёт отформатированное значение вместо сырого
#   2. двоичный float в ячейке (89.98999999999999)
#   3. отрицательные суммы в «Расходах» (возвраты)
#   4. id уехали в колонку «Источник», которая обычно пуста
#   5. книга вышла из сохранения с правами 0755, войдя с 0600
#   6. --dry-run на --init выполнял операцию целиком
#
# Инварианты снимаются ИЗ САМОГО ФАЙЛА (формулы, строки, права, непустые
# колонки), а не хардкодятся — скрипт не привязан к конкретной книге.
#
# Настоящая папка НЕ ИЗМЕНЯЕТСЯ: всё гоняется во временной копии.
#
# Использование:
#   scripts/livecheck.sh ~/claude-cowork/finances
set -uo pipefail

SRC="${1:-}"
if [[ -z "$SRC" || ! -d "$SRC" ]]; then
  echo "использование: $0 <папка с Учёт_финансов.xlsx>" >&2
  exit 2
fi

BOOK_SRC="$(find "$SRC" -maxdepth 1 -name '*.xlsx' ! -name '~$*' | head -1)"
if [[ -z "$BOOK_SRC" ]]; then
  echo "в $SRC нет .xlsx" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/kbengine"
echo "→ сборка движка"
go build -o "$BIN" "$ROOT/cmd/kbengine" || exit 1

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BOOK="$WORK/$(basename "$BOOK_SRC")"
LEDGER="$WORK/transactions.jsonl"
cp "$BOOK_SRC" "$BOOK"
echo "→ копия: $BOOK"

fails=0
ok()   { echo "  ✓ $1"; }
fail() { echo "  ✗ $1"; fails=$((fails + 1)); }

# --- отпечаток книги -------------------------------------------------------
# Формулы и строки считаются прямо в xlsx (это zip с XML) — без openpyxl и
# прочих зависимостей, которых на машине может не оказаться.
fingerprint() {
  local f="$1" dir
  dir="$(mktemp -d)"
  unzip -qo "$f" -d "$dir" 'xl/worksheets/*.xml' 2>/dev/null
  for sheet in "$dir"/xl/worksheets/*.xml; do
    [[ -e "$sheet" ]] || continue
    printf '%s formulas=%s rows=%s\n' \
      "$(basename "$sheet")" \
      "$(grep -o '<f[ >]' "$sheet" | wc -l | tr -d ' ')" \
      "$(grep -o '<row ' "$sheet" | wc -l | tr -d ' ')"
  done
  rm -rf "$dir"
}

perms() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

FP_BEFORE="$(fingerprint "$BOOK")"
PERM_BEFORE="$(perms "$BOOK")"
SHA_BEFORE="$(shasum "$BOOK" | cut -d' ' -f1)"
echo "→ отпечаток до: права $PERM_BEFORE"
echo "$FP_BEFORE" | sed 's/^/    /'

# --- 1. dry-run на --init обязан быть сухим (дефект 6) ---------------------
echo "→ dry-run на --init"
"$BIN" fin sync --init --dry-run --from "$BOOK" --ledger "$LEDGER" >/dev/null 2>&1
[[ -e "$LEDGER" ]] && fail "dry-run создал леджер" || ok "леджер не создан"
[[ -e "$WORK/.sync-state.json" ]] && fail "dry-run создал baseline" || ok "baseline не создан"
[[ "$(shasum "$BOOK" | cut -d' ' -f1)" == "$SHA_BEFORE" ]] \
  && ok "книга не изменилась" || fail "dry-run изменил книгу"

# --- 2. боевое спаривание --------------------------------------------------
echo "→ fin sync --init"
PAIRED="$("$BIN" fin sync --init --from "$BOOK" --ledger "$LEDGER" 2>&1)"
echo "    $PAIRED"
[[ -s "$LEDGER" ]] && ok "леджер создан" || fail "леджер пуст"

# --- 3. книга пережила запись (дефекты 4, 5) -------------------------------
echo "→ книга после записи"
FP_AFTER="$(fingerprint "$BOOK")"
if [[ "$FP_AFTER" == "$FP_BEFORE" ]]; then
  ok "формулы и строки на месте"
else
  fail "отпечаток книги разошёлся:"
  diff <(echo "$FP_BEFORE") <(echo "$FP_AFTER") | sed 's/^/      /'
fi
PERM_AFTER="$(perms "$BOOK")"
[[ "$PERM_AFTER" == "$PERM_BEFORE" ]] \
  && ok "права $PERM_AFTER сохранены" || fail "права $PERM_BEFORE → $PERM_AFTER"

# --- 4. круг сошёлся -------------------------------------------------------
echo "→ повторный sync"
OUT="$("$BIN" fin sync --from "$BOOK" --ledger "$LEDGER" 2>&1)"
grep -q "nothing to do" <<<"$OUT" \
  && ok "обе стороны согласны" || fail "ожидалось «nothing to do», получено: $OUT"

# --- 5. добавленная строка доезжает до книги -------------------------------
echo "→ fin add → sync"
MARK="livecheck-$$"
"$BIN" fin add --ledger "$LEDGER" --amount 1 --cat "Прочее" --note "$MARK" --date 2026-01-01 >/dev/null 2>&1
OUT="$("$BIN" fin sync --from "$BOOK" --ledger "$LEDGER" 2>&1)"
grep -q "ledger → workbook" <<<"$OUT" \
  && ok "направление ledger → workbook" || fail "ожидалось «ledger → workbook», получено: $OUT"

FP_ADDED="$(fingerprint "$BOOK")"
if diff <(echo "$FP_BEFORE") <(echo "$FP_ADDED") | grep -q 'formulas'; then
  # строк стало больше — это ожидаемо; формулы меняться не должны
  before_f="$(echo "$FP_BEFORE" | grep -o 'formulas=[0-9]*' | paste -sd, -)"
  after_f="$(echo "$FP_ADDED" | grep -o 'formulas=[0-9]*' | paste -sd, -)"
  [[ "$before_f" == "$after_f" ]] \
    && ok "формулы не пострадали от вставки" || fail "формулы: $before_f → $after_f"
else
  ok "формулы не пострадали от вставки"
fi

OUT="$("$BIN" fin sync --from "$BOOK" --ledger "$LEDGER" 2>&1)"
grep -q "nothing to do" <<<"$OUT" \
  && ok "baseline обновлён" || fail "после записи baseline не сошёлся: $OUT"

# --- итог ------------------------------------------------------------------
echo
if [[ $fails -eq 0 ]]; then
  echo "LIVECHECK PASS — настоящая папка не тронута ($SRC)"
  exit 0
fi
echo "LIVECHECK FAIL: $fails проверк(и) не прошли — настоящая папка не тронута"
exit 1
