#!/usr/bin/env bash
# Гейт: настоящие имена людей не должны уезжать в публичный репозиторий.
#
# Заведён 26.08.2026, после того как в тестовых фикстурах нашлись имя, должность
# и СТАТУС ЗАНЯТОСТИ живого человека — «DevOps, уходит, компетенция уходит из
# компании целиком». Соседний гейт (личные финансы) к тому моменту работал уже
# три недели и честно молчал: он стережёт деньги, а утекли люди.
#
# Устройство повторяет финансовый гейт намеренно: маркеры читаются из ЖИВЫХ
# файлов в момент push и в репозитории не хранятся. Список имён коллег,
# закоммиченный в публичный гейт, был бы ровно той утечкой, от которой он стоит.
#
# ⚠️ ЧЕГО ЭТОТ ГЕЙТ НЕ ВИДИТ — сказано вслух, а не оставлено додумывать:
#   • Людей, которых нет в team.json. Тот файл — список ОСТАЮЩИХСЯ, и человек,
#     чей уход как раз и утёк, в нём отсутствовал. Ровно поэтому есть второй
#     источник, KB_NAMES_EXTRA; без него гейт проверяет половину и говорит это.
#   • Латинские транслитерации: «Daniil» в пути модуля github.com/daniil/kb-engine
#     стоит законно в 262 файлах, и ловить его значит выключить гейт за день.
#   • Уже запушенное. История отсюда не переписывается.
#   • Имена короче четырёх букв: слишком легко совпадают внутри других слов.
#     Отброшенные называются вслух.
set -uo pipefail

SELF_TEST=0
[ "${1:-}" = "--self-test" ] && SELF_TEST=1

TEAM="${KB_TEAM:-$HOME/claude-cowork/knowledge-base/_data/team.json}"
EXTRA="${KB_NAMES_EXTRA:-$HOME/claude-cowork/knowledge-base/_data/private-names.txt}"

# Слова-должности, которые стоят в тех же карточках, что и имена. Их держим
# здесь, а не в приватном файле: это не персональные данные, а словарь ролей.
STOP='Генеральный|Продажники|Телемаркетологи|Заказчик|Разработчики|Разработчик|Отдел|Продажник|Трекер|Команда|Роли|Состав'

collect_names() {
  local team="$1" extra="$2"
  {
    if [ -f "$team" ]; then
      python3 - "$team" <<'PY'
import json, sys
def cards(o, sens=False):
    if isinstance(o, dict):
        s = o.get("sensitive", sens)
        for c in o.get("cards", []) or []:
            t = (c.get("title") or "").strip()
            if t and s:
                yield t
        for v in o.values():
            yield from cards(v, s)
    elif isinstance(o, list):
        for x in o:
            yield from cards(x, sens)
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print("!ERR\t%s" % e); raise SystemExit(0)
for t in cards(doc):
    print(t)
PY
    fi
    [ -f "$extra" ] && grep -vE '^\s*(#|$)' "$extra"
  } | tr -d '\r' \
    | grep -vE "^($STOP)$" \
    | grep -vE '[→·]' \
    | python3 -c 'import sys
# ⚠️ Длина считается в СИМВОЛАХ, а не в байтах: кириллическая буква занимает
# два, и awk length() пропускал трёхбуквенное имя как шестибайтовое.
for l in sys.stdin:
    t = l.strip()
    if t and len(t.split()) == 1 and len(t) >= 4:
        print(t)' \
    | sort -u
}

report_dropped() {
  local team="$1" extra="$2"
  local short
  short="$( { [ -f "$team" ] && python3 -c "
import json,sys
def cards(o,s=False):
    if isinstance(o,dict):
        s=o.get('sensitive',s)
        for c in o.get('cards',[]) or []:
            t=(c.get('title') or '').strip()
            if t and s: yield t
        for v in o.values(): yield from cards(v,s)
    elif isinstance(o,list):
        for x in o: yield from cards(x,s)
for t in cards(json.load(open('$team',encoding='utf-8'))): print(t)
" 2>/dev/null; [ -f "$extra" ] && grep -vE '^\s*(#|$)' "$extra"; } \
    | grep -vE '[→·]' | python3 -c 'import sys
for l in sys.stdin:
    t = l.strip()
    if t and len(t.split()) == 1 and len(t) < 4:
        print(t)' | sort -u | paste -sd' ' -)"
  [ -n "$short" ] && echo "  ⚠ короче четырёх букв, НЕ проверяются: $short"
  [ ! -f "$extra" ] && echo "  ⚠ файла $extra нет — ушедшие коллеги не проверяются (KB_NAMES_EXTRA)"
  return 0
}

scan() {
  local base="$1" names="$2"
  [ -z "$names" ] && return 0
  local pat; pat="$(mktemp)"; printf '%s\n' "$names" > "$pat"
  local hits msgs
  hits="$(git diff --unified=0 "$base...HEAD" 2>/dev/null | grep '^+' | grep -F -f "$pat" | head -8 || true)"
  msgs="$(git log --format='%s%n%b' "$base..HEAD" 2>/dev/null | grep -F -f "$pat" | head -5 || true)"
  rm -f "$pat"
  printf '%s\n%s' "$hits" "$msgs" | grep -q '[^[:space:]]' && { printf '%s\n%s\n' "$hits" "$msgs"; return 1; }
  return 0
}

# ------------------------------------------------------------- самопроверка
if [ "$SELF_TEST" = "1" ]; then
  fail=0
  tmp="$(mktemp -d)"
  cat > "$tmp/team.json" <<'JSON'
{"sections":[{"title":"Состав","sensitive":true,"cards":[
  {"title":"Тестимя","meta":"роль"},
  {"title":"Ким","meta":"короткое"},
  {"title":"Генеральный","meta":"должность, не имя"},
  {"title":"А → Б","meta":"ребро, не имя"}]}]}
JSON
  printf '# ушедшие\nВторойчеловек\n' > "$tmp/extra.txt"

  names="$(collect_names "$tmp/team.json" "$tmp/extra.txt")"

  echo "$names" | grep -qx 'Тестимя'       || { echo "  ✘ имя из team.json не собрано"; fail=1; }
  echo "$names" | grep -qx 'Второйчеловек' || { echo "  ✘ имя из второго источника не собрано"; fail=1; }
  echo "$names" | grep -qx 'Ким'           && { echo "  ✘ короткое имя попало в маркеры"; fail=1; }
  echo "$names" | grep -qx 'Генеральный'   && { echo "  ✘ должность принята за имя"; fail=1; }
  echo "$names" | grep -q  '→'             && { echo "  ✘ ребро графа принято за имя"; fail=1; }

  # Обе стороны на настоящем git: краснеет на подсадке, молчит на чистом.
  repo="$tmp/repo"; mkdir -p "$repo"; cd "$repo"
  git init -q . && git config user.email t@example.com && git config user.name t
  echo "чисто" > a.txt && git add -A && git -c commit.gpgsign=false commit -q -m base
  base="$(git rev-parse HEAD)"
  echo "карточка сотрудника: Тестимя, уходит" > b.txt && git add -A
  git -c commit.gpgsign=false commit -q -m "подсадка"
  if scan "$base" "$names" >/dev/null; then echo "  ✘ подсадка имени НЕ поймана"; fail=1; fi

  git checkout -q -b clean "$base"
  echo "выдуманное: Роль-А" > c.txt && git add -A
  git -c commit.gpgsign=false commit -q -m "чистый коммит"
  if ! scan "$base" "$names" >/dev/null; then echo "  ✘ ложная тревога на чистом коммите"; fail=1; fi

  # Третья половина: без источников гейт обязан СКАЗАТЬ, а не молча пропустить.
  empty="$(collect_names "$tmp/нет.json" "$tmp/нет.txt")"
  [ -n "$empty" ] && { echo "  ✘ маркеры взялись из несуществующих файлов"; fail=1; }

  cd / && rm -rf "$tmp"
  if [ "$fail" != "0" ]; then echo "✘ no-people-names --self-test: провал"; exit 1; fi
  echo "✓ no-people-names --self-test: 5 правил отбора, обе стороны на настоящем git, пустой источник не молчит"
  exit 0
fi

# ------------------------------------------------------------- боевой прогон
if [ "${NAMES_SKIP:-0}" = "1" ]; then
  echo "⚠ проверка имён пропущена (NAMES_SKIP=1)"
  exit 0
fi

base="origin/main"
git rev-parse --verify --quiet "$base" >/dev/null || { echo "⚠ нет $base — проверка имён пропущена"; exit 0; }

if [ ! -f "$TEAM" ] && [ ! -f "$EXTRA" ]; then
  echo "⚠ ни $TEAM, ни $EXTRA не найдены — имена НЕ проверялись (KB_TEAM / KB_NAMES_EXTRA)"
  exit 0
fi

names="$(collect_names "$TEAM" "$EXTRA")"
if [ -z "$names" ]; then
  echo "⚠ источники есть, но ни одного имени не собрано — проверять нечего"
  report_dropped "$TEAM" "$EXTRA"
  exit 0
fi

if ! out="$(scan "$base" "$names")"; then
  echo "✘ в изменениях есть НАСТОЯЩИЕ ИМЕНА ЛЮДЕЙ — репозиторий публичный"
  echo "$out" | sed 's/^/    /'
  echo "  Заменить ролями (Лид, Продакт, Техлид, Инженер): фикстуры проверяют форму, а не людей."
  echo "  Осознанно: NAMES_SKIP=1 git push"
  exit 1
fi

report_dropped "$TEAM" "$EXTRA"
exit 0
