#!/usr/bin/env bash
#
# Гейт карт архитектуры: прогоняет валидатор по КАЖДОЙ карте репозитория.
#
# Заведён после того, как валидатор одиннадцать правил и тринадцать подсадок лжи
# прожил, не запускаясь ничем: ни CI, ни хуки его не звали (замер грепом по
# scripts, .github и justfile — пусто). Ровно поэтому карта движка отстала на 36
# коммитов кода незамеченной: проверка, которую никто не запускает, от
# несуществующей неотличима.
#
# Карты ищутся С ДИСКА, а не перечисляются именами. Одно имя здесь уже было
# ловушкой в самом валидаторе: pathspec ":(exclude)docs/architecture-map" не
# покрывает соседнюю "docs/architecture-map-cloud", и правка одной карты
# объявляла бы отставшей другую. Новая карта попадает под гейт тем, что она
# появилась, а не тем, что кто-то вспомнил дописать её сюда.
#
# H9 (штамп коммита) идёт предупреждением, а не отказом: внутри pull request
# карта не может назвать коммит, которого ещё нет, и жёсткий H9 красил бы каждое
# изменение кода. Сдвиг кода ловится не штампом, а якорями — они жёсткие.
#
# Usage: scripts/gates/arch-map.sh [--self-test]

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

validator="docs/architecture-map/validate.py"
[ -f "$validator" ] || { echo "✘ валидатора нет: $validator"; exit 1; }

maps() { find docs -name map.json -type f | sort; }

# Уборка объявлена здесь, а не внутри функции: trap срабатывает НА ВЫХОДЕ ИЗ
# СКРИПТА, когда локальной переменной функции уже нет, и при set -u это роняет
# скрипт после успешной работы. Ошибка уже случалась в соседнем гейте.
tmpdir=""
trap 'rm -rf "${tmpdir:-}"' EXIT

# Проверка проверки. Зелёный гейт, который никогда не краснел, ничего не значит,
# поэтому здесь подсаживается ложь того самого класса, ради которого гейт нужен:
# якорь на файл, которого нет. Гейт обязан упасть на подсадке и пройти на
# нетронутой карте — обе половины, иначе «падает всегда» тоже сойдёт за работу.
#
# Чего самопроверка НЕ покрывает: подсадка идёт в ПЕРВУЮ карту списка, поэтому
# она доказывает работу валидатора, а не то, что цикл дошёл до каждой карты.
# Второе держится счётчиком found и отказом при нуле карт.
self_test() {
  local map first rc
  first="$(maps | head -1)"
  [ -n "$first" ] || { echo "✘ карт не найдено — гейту нечего проверять"; exit 1; }

  echo "→ отрицательный контроль валидатора (подсадки лжи)"
  python3 "$validator" "$first" . --self-test

  tmpdir="$(mktemp -d)"
  map="$tmpdir/map.json"
  python3 - "$first" "$map" <<'PY'
import json, sys
m = json.load(open(sys.argv[1], encoding="utf-8"))
m["flows"][0]["steps"][0]["source"] = "internal/etogo-fajla-net.go:1"
json.dump(m, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False)
PY

  rc=0
  python3 "$validator" "$map" . --commit-warn >/dev/null 2>&1 || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "✘ самопроверка: подсаженный якорь в никуда НЕ пойман — гейт ничего не проверяет"
    exit 1
  fi
  echo "  ✓ подсаженный якорь пойман (код $rc)"

  rc=0
  python3 "$validator" "$first" . --commit-warn >/dev/null 2>&1 || rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "✘ самопроверка: гейт падает на НЕТРОНУТОЙ карте — он падает всегда, а не по делу"
    exit 1
  fi
  echo "  ✓ на нетронутой карте молчит"
}

if [ "${1:-}" = "--self-test" ]; then
  self_test
  exit 0
fi

failed=0
found=0
while IFS= read -r map; do
  found=$((found + 1))
  echo "→ $map"
  python3 "$validator" "$map" . --commit-warn || failed=$((failed + 1))

  # Насколько карта отстала — числом, а не ощущением. Это справка, а не отказ:
  # отставание на коммит-другой нормально между правкой кода и правкой карты.
  claimed="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1],encoding='utf-8')).get('commit',''))" "$map")"
  if [ -n "$claimed" ] && git cat-file -e "$claimed^{commit}" 2>/dev/null; then
    excludes=()
    while IFS= read -r d; do excludes+=(":(exclude)$d"); done < <(maps | xargs -n1 dirname | sort -u)
    behind="$(git log --oneline "$claimed..HEAD" -- . "${excludes[@]}" | wc -l | tr -d ' ')"
    echo "   отставание штампа: $behind коммит(ов) кода"
  else
    echo "   отставание штампа: не посчитать — заявленного коммита нет в этой копии"
  fi
done < <(maps)

if [ "$found" -eq 0 ]; then
  echo "✘ ни одной карты не найдено под docs/ — гейт прошёл бы впустую"
  exit 1
fi

if [ "$failed" -gt 0 ]; then
  echo "✘ карт с нарушениями: $failed из $found"
  exit 1
fi
echo "✓ карт проверено: $found, нарушений нет"
