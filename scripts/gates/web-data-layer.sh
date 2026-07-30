#!/usr/bin/env bash
# Гейт слоёв фронта: запросы живут в api.ts, загрузка — в hooks/.
#
# До этого гейта тройка «загрузка / ошибка / данные» лежала в четырёх
# компонентах, каждый со своей трактовкой ошибки. Расползлась она не потому,
# что кто-то решил так спроектировать, а потому что скопировать useEffect в
# новый вид дешевле, чем вспомнить, где лежит хук. Дешевизну и убираем.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/frontend/src"

fail=0

# ThemeToggle синхронизирует класс на <html> — это эффект про DOM, а не про
# загрузку, и в hooks/ ему делать нечего.
offenders="$(grep -rln --include='*.tsx' --include='*.ts' -E '^\s*useEffect\(' . \
  | grep -v -e '^\./hooks/' -e '^\./components/ThemeToggle\.tsx$' || true)"
if [ -n "$offenders" ]; then
  echo "✘ useEffect вне hooks/ (загрузка данных должна идти через useResource):"
  echo "$offenders" | sed 's/^/    /'
  fail=1
fi

# fetch мимо типизированного клиента обходит и обработку не-200, и типы.
callers="$(grep -rln --include='*.tsx' --include='*.ts' -E '(^|[^.[:alnum:]])fetch\(' . \
  | grep -v -e '^\./api\.ts$' || true)"
if [ -n "$callers" ]; then
  echo "✘ fetch( вне api.ts (запрос должен идти через типизированный клиент):"
  echo "$callers" | sed 's/^/    /'
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "✓ web-data-layer: запросы в api.ts, загрузка в hooks/"
