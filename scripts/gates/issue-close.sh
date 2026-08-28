#!/usr/bin/env bash
# Гейт закрытия issue: ловит ОБЕ половины немой поломки.
#
# Половина первая — русское слово молча НЕ закрывает. GitHub понимает только
# английские ключевые слова (Closes / Fixes / Resolves). Написав «Закрывает
# #318», автор получает смерженный PR и открытую issue, а трекер начинает
# врать в сторону «ещё не сделано». Замер 28.08.2026: ровно так и вышло, issue
# закрывали руками через сутки, и заметили случайно.
#
# Половина вторая, и она опаснее — английское слово молча ЗАКРЫВАЕТ
# недоделанное. Автозакрытие срабатывает на мерж, а не на готовность: если у
# issue есть пункты приёмки и часть не отмечена, она всё равно закроется, и
# невыполненное исчезнет из трекера вместе с ней. Здесь трекер врёт в другую
# сторону — «сделано», — и это дороже, потому что никто не приходит проверять
# после «выполнено».
#
# Поэтому гейт требует: либо ключевого слова нет вовсе и закрытие остаётся
# отдельным осознанным шагом, либо оно английское И все пункты приёмки в issue
# отмечены.
#
# Использование:
#   scripts/gates/issue-close.sh <pr-number>     проверить PR (нужен gh)
#   scripts/gates/issue-close.sh --body FILE     проверить тело из файла
#   scripts/gates/issue-close.sh --self-test     прогон на подсадках, без сети
#
# Переменные:
#   ISSUE_FIXTURES=DIR  брать тела issue из DIR/<N>.md вместо сети (для тестов)
set -euo pipefail

fail=0
note() { printf '  %s\n' "$1"; }
bad() { printf '✘ %s\n' "$1"; fail=1; }

# fetch_issue_body печатает тело issue. Источник подменяем переменной, чтобы
# самопроверка шла без сети: гейт, который нельзя прогнать локально, проверяют
# только в CI, то есть уже после отправки.
fetch_issue_body() {
  local n="$1"
  if [[ -n "${ISSUE_FIXTURES:-}" ]]; then
    [[ -f "$ISSUE_FIXTURES/$n.md" ]] && cat "$ISSUE_FIXTURES/$n.md" || echo "__MISSING__"
    return
  fi
  gh issue view "$n" --json body -q .body 2>/dev/null || echo "__MISSING__"
}

check_body() {
  local body="$1"

  # 1. Русские псевдо-ключевые слова: выглядят как автозакрытие и им не являются.
  local ru
  ru=$(printf '%s' "$body" | grep -oiE '(закрывает|закрыто|закрывается|чинит|решает|исправляет|фиксит|правит)[[:space:]]+#[0-9]+' || true)
  if [[ -n "$ru" ]]; then
    bad "русское слово НЕ закроет issue — GitHub понимает только Closes/Fixes/Resolves:"
    printf '%s\n' "$ru" | sed 's/^/    /'
    note "либо поставьте английское слово (и тогда все пункты приёмки должны быть отмечены),"
    note "либо оставьте ссылку без слова и закройте issue руками после проверки в main."
  fi

  # 2. Английские ключевые слова: закроют на мерже, готова issue или нет.
  local nums
  nums=$(printf '%s' "$body" | grep -oiE '(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]+#[0-9]+' | grep -oE '[0-9]+' | sort -u || true)
  [[ -z "$nums" ]] && return

  local n ibody open_boxes total_boxes
  for n in $nums; do
    ibody=$(fetch_issue_body "$n")
    if [[ "$ibody" == "__MISSING__" ]]; then
      note "#$n: тело issue не получено — пункты приёмки НЕ проверены"
      continue
    fi
    total_boxes=$(printf '%s' "$ibody" | grep -cE '^[[:space:]]*[-*][[:space:]]+\[[ xX]\]' || true)
    open_boxes=$(printf '%s' "$ibody" | grep -cE '^[[:space:]]*[-*][[:space:]]+\[[[:space:]]\]' || true)
    if [[ "$total_boxes" -eq 0 ]]; then
      note "#$n: пунктов приёмки нет — закрытие по мержу не с чем сверить"
      continue
    fi
    if [[ "$open_boxes" -gt 0 ]]; then
      bad "#$n закроется на мерже, но $open_boxes из $total_boxes пунктов приёмки не отмечены:"
      printf '%s' "$ibody" | grep -E '^[[:space:]]*[-*][[:space:]]+\[[[:space:]]\]' | sed 's/^/    /'
      note "отметьте выполненное или уберите ключевое слово — закрытие тогда останется вашим решением."
    else
      note "#$n: все $total_boxes пунктов приёмки отмечены — автозакрытие законно"
    fi
  done
}

self_test() {
  local dir; dir=$(mktemp -d); local rc=0
  mkdir -p "$dir/iss"
  printf '%s\n' '## Приёмка' '- [x] сделано одно' '- [x] сделано два' > "$dir/iss/5.md"
  printf '%s\n' '## Приёмка' '- [x] сделано одно' '- [ ] НЕ сделано два' > "$dir/iss/6.md"
  printf '%s\n' 'Обычный текст без пунктов.' > "$dir/iss/7.md"
  export ISSUE_FIXTURES="$dir/iss"

  probe() { # имя · тело · ожидаемый код
    local name="$1" body="$2" want="$3" got=0
    printf '%s' "$body" > "$dir/body.md"
    ( fail=0; check_body "$(cat "$dir/body.md")" >/dev/null 2>&1; exit $fail ) || got=1
    if [[ "$got" -ne "$want" ]]; then
      printf '  ✘ %s: код %s, ожидался %s\n' "$name" "$got" "$want"; rc=1
    else
      printf '  ✔ %s\n' "$name"
    fi
  }

  probe "русское слово — обязан упасть"            'Закрывает #5'  1
  probe "чинит #N — тоже русское"                  'Чинит #6'      1
  probe "английское + все пункты отмечены"         'Closes #5'     0
  probe "английское + есть неотмеченный"           'Closes #6'     1
  probe "Fixes у issue без пунктов приёмки"        'Fixes #7'      0
  probe "ссылка без ключевого слова"               'см. #6'        0
  probe "ни одной ссылки"                          'просто текст'  0
  probe "английское, но issue не получена"         'Closes #999'   0

  rm -rf "$dir"
  [[ "$rc" -eq 0 ]] && echo "самопроверка: 8 из 8" || echo "самопроверка ПРОВАЛЕНА"
  return "$rc"
}

main() {
  case "${1:-}" in
    --self-test) self_test; exit $? ;;
    --body) check_body "$(cat "$2")" ;;
    "") echo "issue-close: нужен номер PR, --body FILE или --self-test" >&2; exit 2 ;;
    *) check_body "$(gh pr view "$1" --json body -q .body)" ;;
  esac
  if [[ "$fail" -eq 0 ]]; then
    echo "✓ закрытие issue: нарушений нет"
  else
    echo "✘ гейт закрытия issue не пройден"
  fi
  exit "$fail"
}

main "$@"
