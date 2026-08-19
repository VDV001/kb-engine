#!/usr/bin/env bash
# Сверяет состав выпуска с историей: какие коммиты после тега требовали записи
# в CHANGELOG и её не оставили.
#
# Существует потому, что pre-push стоит на машине разработчика и ветку, ушедшую
# мимо него, уже не догонит: PR #223 прошёл по букве прежнего правила, и пропуск
# нашёлся руками при сборке 0.22.0 — сверкой с `git log v0.21.0..main`. Это та
# самая сверка, только не по памяти.
#
# ⚠️ Инструмент называет, чего он НЕ проверял: он смотрит на КОММИТЫ, а не на
# текст раздела [Unreleased]. Запись, дописанная в другом коммите той же ветки,
# засчитывается — это правильно; запись, дописанная не про то, — нет, и такую
# он не отличит.
#
# Использование: release-scope.sh <прошлый тег> [ветка]
set -euo pipefail

here="$(dirname "$0")"
tag="${1:-}"
branch="${2:-main}"

if [ -z "$tag" ]; then
  echo "usage: release-scope.sh <прошлый тег> [ветка]" >&2
  exit 2
fi
if ! git rev-parse --verify --quiet "$tag" >/dev/null; then
  echo "✘ тега $tag нет — сверять не с чем" >&2
  exit 2
fi

missing=0
checked=0
while read -r sha; do
  [ -n "$sha" ] || continue
  culprit="$("$here/changelog-scope.sh" "$sha^..$sha" || true)"
  [ -n "$culprit" ] || continue
  checked=$((checked + 1))
  if ! git show --name-only --format="" "$sha" | grep -q '^CHANGELOG.md$'; then
    missing=$((missing + 1))
    printf '  ✘ %s\n     тронут %s, записи в журнале нет\n' \
      "$(git log -1 --format='%h %s' "$sha" | cut -c1-78)" "$culprit"
  fi
done < <(git log "$tag..$branch" --format="%H")

echo "проверено коммитов, менявших поведение: $checked"
if [ "$missing" -gt 0 ]; then
  echo "✘ без записи в журнале: $missing — состав выпуска неполон"
  exit 1
fi
echo "✓ каждый поведенческий коммит после $tag оставил запись"
