#!/usr/bin/env bash
# Гейт: статусная таблица README не расходится с источником.
#
# Источник ОДИН — срез Capabilities() в internal/adapter/httpapi/capabilities.go.
# Его же читает вкладка About. README держит копию таблицы для читателя гитхаба,
# и без этого гейта копия разошлась бы молча — тот же дефект «число живёт
# дважды», что ловит verify-claims.
#
# Проверяется в обе стороны: каждая возможность источника есть в README с ТЕМ ЖЕ
# статусом, и в README нет строки-возможности, которой нет в источнике.
#
# Использование:
#   scripts/gates/capabilities.sh              сверка живых файлов
#   scripts/gates/capabilities.sh --self-test  прогон на подсадках
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"

src="internal/adapter/httpapi/capabilities.go"
readme="README.md"

# pairs_from_src <file> — «имя<TAB>статус» из среза Capabilities().
pairs_from_src() {
  grep -oE '\{"[^"]+", "(stable|experimental|roadmap)", "[^"]+"\}' "$1" \
    | sed -E 's/^\{"([^"]+)", "([^"]+)", .*/\1\t\2/'
}

# pairs_from_readme <file> — «имя<TAB>статус» из строк таблицы README.
# Строка вида «| Имя | ✅ stable | замечание |»; бейдж-эмодзи отбрасываем.
pairs_from_readme() {
  grep -E '^\| .+ \| (✅|⚠️|🚧) (stable|experimental|roadmap) \|' "$1" \
    | sed -E 's/^\| (.+) \| [^ ]+ (stable|experimental|roadmap) \|.*/\1\t\2/'
}

check() {
  local src_file="$1" readme_file="$2" fail=0
  local s r
  s="$(pairs_from_src "$src_file" | sort)"
  r="$(pairs_from_readme "$readme_file" | sort)"
  # В источнике, но не в README (или другой статус):
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! grep -qxF "$line" <<<"$r"; then
      echo "✘ в источнике есть, а в README нет (или иной статус): ${line//$'\t'/ → }"
      fail=1
    fi
  done <<<"$s"
  # В README, но не в источнике:
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! grep -qxF "$line" <<<"$s"; then
      echo "✘ в README есть строка-возможность, которой нет в источнике: ${line//$'\t'/ → }"
      fail=1
    fi
  done <<<"$r"
  return "$fail"
}

if [ "${1:-}" = "--self-test" ]; then
  fail=0
  tmp_src="$(mktemp)"; tmp_rd="$(mktemp)"; trap 'rm -f "$tmp_src" "$tmp_rd"' EXIT

  # 1. живые файлы согласованы
  if ! check "$src" "$readme" >/dev/null; then
    echo "✘ self-test: живые README и источник разошлись"; fail=1
  fi

  # 2. подсадка: в README у возможности сменён статус → гейт обязан назвать
  cp "$src" "$tmp_src"
  sed -E 's/(Метрики Prometheus \| )✅ stable/\1🚧 roadmap/' "$readme" > "$tmp_rd"
  out="$(check "$tmp_src" "$tmp_rd" 2>&1)" && { echo "✘ self-test: смена статуса в README не поймана"; fail=1; }
  case "$out" in *"Метрики Prometheus"*) ;; *) echo "✘ self-test: расхождение не названо: $out"; fail=1;; esac

  # 3. подсадка: в источник добавлена возможность, в README её нет → поймать
  cp "$readme" "$tmp_rd"
  sed -E 's#(return \[\]Capability\{)#\1\n\t\t{"Выдуманная фича", "stable", "нет в README"},#' "$src" > "$tmp_src"
  out="$(check "$tmp_src" "$tmp_rd" 2>&1)" && { echo "✘ self-test: новая возможность без строки в README не поймана"; fail=1; }
  case "$out" in *"Выдуманная фича"*) ;; *) echo "✘ self-test: пропажа строки не названа: $out"; fail=1;; esac

  if [ "$fail" -ne 0 ]; then exit 1; fi
  echo "✓ capabilities --self-test: живые согласованы; смена статуса и лишняя возможность ловятся с именем"
  exit 0
fi

if check "$src" "$readme"; then
  n="$(pairs_from_src "$src" | grep -c . || true)"
  echo "✓ capabilities: README и источник согласованы (${n} возможностей)"
fi
