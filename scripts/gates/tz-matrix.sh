#!/usr/bin/env bash
# Самопроверка матрицы часовых поясов: доказывает, что прогон в крайних зонах
# ловит то, чего прогон в UTC не видит.
#
# Без неё зелёная матрица неотличима от матрицы, не проверяющей ничего: раннеры
# GitHub живут в UTC, и «тесты прошли в Pacific/Midway» само по себе не значит,
# что зона вообще влияла на результат.
#
# Подсаживается настоящий дефект того класса, ради которого правило и появилось
# (v0.18.0): «сегодня», посчитанное в UTC вместо местной зоны. Именно так книга
# переставала читаться между 00:00 и 05:00 по месту.
#
# ⚠️ Почему поясов ДВА и почему проверка требует «хотя бы одного» падения.
# Дефект виден только когда местная дата не совпадает с датой UTC:
#   UTC+14 отличается при UTC-часе 10:00–23:59,
#   UTC-11 отличается при UTC-часе 00:00–10:59.
# Вместе они покрывают сутки целиком, поэтому в ЛЮБОЙ час хотя бы один пояс
# обязан покраснеть — а требовать красноты от обоих значило бы завести проверку,
# зелёную только полдня.
set -euo pipefail

EAST="Pacific/Kiritimati" # UTC+14
WEST="Pacific/Midway"     # UTC-11
TARGET="internal/usecase/finance/duplicate.go"
PKGS=(./internal/usecase/finance/ ./cmd/kbengine/)

if [ "${1:-}" != "--self-test" ]; then
  echo "usage: tz-matrix.sh --self-test" >&2
  exit 2
fi

if ! git diff --quiet -- "$TARGET"; then
  echo "✘ $TARGET изменён — самопроверка правит его и откатила бы вашу работу" >&2
  exit 2
fi
restore() { git checkout -- "$TARGET"; }
trap restore EXIT

echo "→ подсадка: «сегодня» считается в UTC вместо местной зоны"
perl -0pi -e 's/\t\twant = domain\.Day\(time\.Now\(\)\)/\t\twant = domain.Day(time.Now().UTC())/' "$TARGET"
if git diff --quiet -- "$TARGET"; then
  echo "✘ подсадка не применилась — строка изменилась, самопроверка мерит не то" >&2
  exit 1
fi

reds=0
for tz in "$EAST" "$WEST"; do
  if TZ="$tz" go test "${PKGS[@]}" -count=1 >/dev/null 2>&1; then
    echo "  · $tz ($(TZ="$tz" date +%F)): зелено"
  else
    echo "  ✓ $tz ($(TZ="$tz" date +%F)): поймал подсадку"
    reds=$((reds + 1))
  fi
done

if ! TZ=UTC go test "${PKGS[@]}" -count=1 >/dev/null 2>&1; then
  echo "✘ self-test: подсадка красная и в UTC — значит она ловится обычным прогоном," >&2
  echo "  и матрица не доказывает своей полезности. Нужен другой образец дефекта." >&2
  exit 1
fi
echo "  ✓ UTC: зелено — обычный прогон этот дефект не видит"

if [ "$reds" -eq 0 ]; then
  echo "✘ self-test: ни один крайний пояс не покраснел — матрица не ловит зонозависимый дефект" >&2
  exit 1
fi
echo "✓ tz-matrix --self-test: дефект невидим в UTC и пойман крайним поясом ($reds из 2)"
