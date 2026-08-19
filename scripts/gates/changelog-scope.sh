#!/usr/bin/env bash
# Решает один вопрос: обязана ли эта ветка оставить запись в CHANGELOG.md.
#
# Признак — СОСТАВ ДИФФА, а не префикс заголовка. Прежнее правило считало
# коммиты по `^(feat|fix)`, и множество признака расходилось с охраняемым в обе
# стороны, оба раза замерено:
#
#   молчал: PR #223 нёс новую обязательную джобу CI и новый гейт, а коммиты
#           назывались docs(map)/ci(gates) — ноль совпадений, запись не доехала
#           до журнала, нашлось руками при сборке 0.22.0 (issue #228);
#   краснел: коммит, восстанавливающий снесённые тесты, трогает только
#           *_test.go и поведения движка не меняет — писать в журнал нечего,
#           а гейт требовал и приучал жать CHANGELOG_SKIP.
#
# Правило: запись нужна, если ветка тронула хоть один файл ВНЕ молчаливого
# множества — тестов, документации и файлов зависимостей.
#
# ⚠️ Чего этот признак не умеет: отличить код, у которого ещё нет ни одного
# вызывающего (новый тип, который никто не создаёт), от работающего. Такая
# ветка требование получит, и обойти её честно — через CHANGELOG_SKIP=1.
# Диффом это неразличимо, и притворяться иначе гейт не должен.
#
# Использование:
#   changelog-scope.sh <base>..<head>   → 0 «запись нужна», 1 «не нужна»
#   changelog-scope.sh --self-test      → прогон на настоящих коммитах истории
set -euo pipefail

# silent — пути, изменение которых само по себе поведения проекта не меняет.
#
# Зависимости здесь намеренно: их поднимает бот пачками, и требование записи с
# каждого bump'а — это шум, от которого гейт начинают обходить рефлексом.
# Обновление, которое ДЕЙСТВИТЕЛЬНО стоит записать (закрытая уязвимость),
# приходит вместе с правкой кода или конфигурации сборки и попадёт под правило.
silent_path() {
  case "$1" in
    *_test.go|*.test.ts|*.test.tsx) return 0 ;;
    docs/*|*.md) return 0 ;;
    go.mod|go.sum) return 0 ;;
    *package.json|*package-lock.json|*.lock) return 0 ;;
    *) return 1 ;;
  esac
}

# needs_entry печатает имя первого файла, требующего записи, и возвращает 0.
needs_entry() {
  local range="$1" f
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    if ! silent_path "$f"; then
      echo "$f"
      return 0
    fi
  done < <(git diff --name-only "$range")
  return 1
}

self_test() {
  local fail=0

  # Настоящие случаи из истории, оба направления. Гейт, у которого видели
  # только одну сторону, неотличим от всегда-требующего.
  local -a must_require=(
    "1370ea8 PR #223: новая джоба CI и новый гейт под именами docs/ci"
    "56d242a журнал прогонов: код движка"
  )
  local -a must_stay_silent=(
    "172b6da chore(deps): только package.json и лок"
    "36f119e восстановление тестов: только *_test.go"
  )

  local entry sha why
  for c in "${must_require[@]}"; do
    sha="${c%% *}"; why="${c#* }"
    if entry="$(needs_entry "$sha^..$sha")"; then
      echo "  ✓ требует записи: $why (первым назван $entry)"
    else
      echo "  ✘ self-test: гейт промолчал там, где обязан требовать — $why"
      fail=1
    fi
  done
  for c in "${must_stay_silent[@]}"; do
    sha="${c%% *}"; why="${c#* }"
    if entry="$(needs_entry "$sha^..$sha")"; then
      echo "  ✘ self-test: гейт потребовал запись там, где менять нечего — $why (назван $entry)"
      fail=1
    else
      echo "  ✓ молчит: $why"
    fi
  done

  if [ "$fail" -ne 0 ]; then
    echo "✘ changelog-scope --self-test провален"
    exit 1
  fi
  echo "✓ changelog-scope --self-test: обе стороны на настоящих коммитах"
}

if [ "${1:-}" = "--self-test" ]; then
  self_test
  exit 0
fi

if [ $# -ne 1 ]; then
  echo "usage: changelog-scope.sh <base>..<head> | --self-test" >&2
  exit 2
fi

needs_entry "$1"
