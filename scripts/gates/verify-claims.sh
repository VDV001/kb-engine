#!/usr/bin/env bash
# Гейт «каждое число в README пересчитывается из источника».
#
# Идея из elder-plinius/T3MP3ST, где `npm run verify-claims` пересчитывает
# каждое число README, 27/27, и «число, которое нельзя воспроизвести, не
# публикуется». Здесь проверяемая claim пока одна — размер образа, — но правило
# заводится сразу с самопроверкой, чтобы новая claim добавлялась строкой.
#
# ⚠️ Мерить надо ТОТ ЖЕ артефакт, что называет README. README говорит «The image
# is a ~13.5 MB … binary», и это ОБРАЗ, собранный с `-trimpath -ldflags="-s -w"`
# CGO=0 (Dockerfile), а не `go build` (тот 19 МБ — другой артефакт). Проверять
# `go build` значило бы повторить ошибку замера, из-за которой этот гейт чуть не
# завёлся на ложном поводе: README был точен, а «устарел» — мой неверный замер.
#
# Использование:
#   scripts/gates/verify-claims.sh                 размер берётся у docker
#   VERIFY_IMAGE_MB=13.5 scripts/gates/verify-claims.sh   размер задан (для CI,
#                                                   где образ уже собран джобой)
#   scripts/gates/verify-claims.sh --self-test     прогон на подсадках
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"

readme="README.md"
# Допуск: README округляет и пишет «~», а образ ещё и зависит от архитектуры —
# amd64 крупнее arm64 примерно на мегабайт (замерено: 17,4 против 16,3). ±1,5 МБ
# покрывает и это, и мелкий рост с кодом; шире прятало бы настоящий дрейф.
tol_mb="1.5"

# claimed_mb <readme-file> — вытащить заявленный размер образа из прозы.
# Печатает число или пусто, если claim в тексте нет (это отдельный ответ).
claimed_mb() {
  grep -oE 'image is a ~?[0-9]+(\.[0-9]+)? MB' "$1" | head -1 | grep -oE '[0-9]+(\.[0-9]+)?' || true
}

check() {
  local readme_file="$1" actual_mb="$2"
  local claimed
  claimed="$(claimed_mb "$readme_file")"
  if [ -z "$claimed" ]; then
    # Пропажа claim — не молчание: удалённая строка иначе прошла бы как «сходится».
    echo "✘ verify-claims: в ${readme_file} нет заявленного размера образа (искали 'image is a ~N MB')"
    return 1
  fi
  local diff
  diff="$(awk -v a="$actual_mb" -v c="$claimed" 'BEGIN{d=a-c; if(d<0)d=-d; printf "%.2f", d}')"
  if awk -v d="$diff" -v t="$tol_mb" 'BEGIN{exit !(d>t)}'; then
    echo "✘ verify-claims: README заявляет ${claimed} MB, образ ${actual_mb} MB (расхождение ${diff} MB > ${tol_mb})"
    return 1
  fi
  echo "✓ verify-claims: README ${claimed} MB ≈ образ ${actual_mb} MB (расхождение ${diff} MB)"
}

if [ "${1:-}" = "--self-test" ]; then
  fail=0
  tmp="$(mktemp)"; trap 'rm -f "$tmp"' EXIT

  # 1. верный README + верный размер → зелёный
  printf 'The image is a ~13.5 MB distroless/static binary.\n' > "$tmp"
  if ! check "$tmp" 13.5 >/dev/null; then echo "✘ self-test: точное число названо расхождением"; fail=1; fi

  # 2. устаревшее число в README → красный, и оба числа названы
  printf 'The image is a ~13.5 MB distroless/static binary.\n' > "$tmp"
  out="$(check "$tmp" 19.4 2>&1)" && { echo "✘ self-test: расхождение 13.5↔19.4 не поймано"; fail=1; }
  case "$out" in *13.5*19.4*) ;; *) echo "✘ self-test: не названы оба числа: $out"; fail=1;; esac

  # 3. claim отсутствует вовсе → отдельный красный, не «сходится»
  printf 'No size mentioned here.\n' > "$tmp"
  out="$(check "$tmp" 13.5 2>&1)" && { echo "✘ self-test: отсутствие claim прошло как успех"; fail=1; }
  case "$out" in *"нет заявленного размера"*) ;; *) echo "✘ self-test: пропажа claim не названа: $out"; fail=1;; esac

  # 4. в пределах допуска → зелёный (README округляет, образ чуть подрос)
  printf 'The image is a ~13.5 MB distroless/static binary.\n' > "$tmp"
  if ! check "$tmp" 14.2 >/dev/null; then echo "✘ self-test: 14.2 против 13.5 в пределах допуска, а названо расхождением"; fail=1; fi

  if [ "$fail" -ne 0 ]; then exit 1; fi
  echo "✓ verify-claims --self-test: точное сходится, устаревшее краснеет с обоими числами, пропажа claim — отдельный отказ, допуск соблюдён"
  exit 0
fi

if [ -n "${VERIFY_IMAGE_MB:-}" ]; then
  actual="$VERIFY_IMAGE_MB"
else
  size="$(docker images kbengine:ci --format '{{.Size}}' 2>/dev/null | head -1)"
  [ -z "$size" ] && size="$(docker images kbengine:dev --format '{{.Size}}' 2>/dev/null | head -1)"
  if [ -z "$size" ]; then
    echo "ℹ образа kbengine:ci/:dev нет — нечего мерить; собери 'docker build -t kbengine:ci .' или задай VERIFY_IMAGE_MB"
    exit 0
  fi
  # docker печатает «13.5MB» / «1.2GB»; приводим к мегабайтам.
  actual="$(awk -v s="$size" 'BEGIN{
    n=s+0; u=s; gsub(/[0-9.]/,"",u);
    if(u=="GB") n*=1024; else if(u=="kB"||u=="KB") n/=1024;
    printf "%.1f", n}')"
fi
check "$readme" "$actual"
