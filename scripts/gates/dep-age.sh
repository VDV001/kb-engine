#!/usr/bin/env bash
# Гейт возраста зависимостей: версия npm-пакета, попавшая в ветку, должна быть
# старше карантина.
#
# Свежая версия — самое опасное окно supply chain: скомпрометированный релиз
# живёт до обнаружения часы-дни. Правило «не ставить пакеты моложе недели» у
# владельца записано с мая, а 13.08.2026 мимо него проехали `@types/node`
# 26.2.0 (5 дней) и `vite` 8.2.1 (6 дней) — обновление от dependabot. Поймал их
# человек, читавший диффы руками.
#
# Почему нативной защиты не хватило, и это замерено, а не выведено:
#
#   npm install vite@8.2.1   при min-release-age=7 → ETARGET, отказ
#   npm ci                   при том же пороге и локе с 8.2.1 → ставит молча
#
# Порог живёт в резолвере, а `npm ci` ничего не резолвит: лок-файл уже сведён.
# Именно так работает CI и именно так приезжает обновление от бота, который
# резолвил на своей стороне. Отсюда третий слой — этот гейт, смотрящий на то,
# что ветка ДОБАВЛЯЕТ в лок.
#
# Порог не задаётся здесь: его говорит сам npm (`npm config get
# min-release-age`), чтобы одно правило не жило двумя числами.
#
# Использование:
#   scripts/gates/dep-age.sh [<base-ref>]
#   scripts/gates/dep-age.sh --ref <sha> <base-ref>   лок из коммита, не с диска
#   scripts/gates/dep-age.sh --self-test              прогон на настоящем случае
#
# Переменные:
#   DEP_AGE_NOW=YYYY-MM-DD   «сегодня» для проверок (иначе системное время)
#   DEP_AGE_THRESHOLD=<дни>  порог, если npm-конфига нет (иначе 7)
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

lock="frontend/package-lock.json"

# Самопроверка на настоящем случае, а не на выдуманном: коммит 172b6da — это
# смерженный PR #194, тот самый, что провёз мимо правила две свежие версии.
# Проверка, у которой успех выглядит как отсутствие ошибки, обязана уметь
# падать на настоящем образце того, от чего защищает.
if [ "${1:-}" = "--self-test" ]; then
  fixture=172b6da1af5aeb1de5849377a0b684819fca31db
  fail=0
  out="$(DEP_AGE_NOW=2026-08-13 "$0" --ref "$fixture" "$fixture^" 2>&1)" && rc=0 || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "✘ self-test: на PR #194 гейт промолчал"
    fail=1
  fi
  for pkg in '@types/node@26.2.0' 'vite@8.2.1'; do
    case "$out" in
      *"$pkg"*) ;;
      *) echo "✘ self-test: ${pkg} не назван"; fail=1 ;;
    esac
  done
  # Тот же коммит неделей позже обязан быть зелёным — иначе гейт красит всё
  # подряд, и его перестанут читать.
  if ! DEP_AGE_NOW=2026-08-20 "$0" --ref "$fixture" "$fixture^" >/dev/null 2>&1; then
    echo "✘ self-test: те же версии через неделю всё ещё красные"
    fail=1
  fi
  # Вторая ось: новая ВЕРСИЯ знакомого пакета и ИМЯ, которого в проекте не было,
  # — разные события, и возраст говорит только про первое. Образцы настоящие:
  #   172b6da — 22 новые версии, ноль новых имён (обычный bump);
  #   c95790e — 27 новых имён, столько же версий.
  # Замер, из-за которого случай заведён: у всех четырёх коммитов kb-engine с
  # новыми именами лок в базе уже БЫЛ, а один из них (9b5e0c9) — обычный bump от
  # dependabot, привёзший 20 платформенных бинарников TypeScript. То есть новое
  # имя приезжает ровно тем путём, который гейт считает безопасным.
  names_fixture=c95790e582055176cd99932f1f0f37bcb89541ae
  names_out="$(DEP_AGE_NOW=2026-08-13 "$0" --ref "$names_fixture" "$names_fixture^" 2>&1)" || true
  case "$names_out" in
    *"которых в проекте не было"*) ;;
    *) echo "✘ self-test: на ${names_fixture:0:7} новые имена не названы"; fail=1 ;;
  esac
  case "$names_out" in
    *'@standard-schema/spec'*) ;;
    *) echo "✘ self-test: новое имя не названо поимённо"; fail=1 ;;
  esac
  # Обычный bump обязан молчать про имена — детектор, срабатывающий всегда,
  # ничего не различает.
  case "$out" in
    *"которых в проекте не было"*)
      echo "✘ self-test: на bump без новых имён строка всё равно напечатана"; fail=1 ;;
  esac
  # Отрицательный контроль: гейт со сломанным сравнением имён обязан уронить
  # самопроверку. Без него «строка не напечаталась» и «сравнивать разучились»
  # выглядят одинаково.
  broken="$(mktemp)"
  sed 's/!had_names\.has(name)/false/' "$0" > "$broken"
  chmod +x "$broken"
  broken_out="$(DEP_AGE_NOW=2026-08-13 "$broken" --ref "$names_fixture" "$names_fixture^" 2>&1)" || true
  rm -f "$broken"
  case "$broken_out" in
    *"которых в проекте не было"*)
      echo "✘ self-test: подсадка «имена не сравниваются» прошла незамеченной"; fail=1 ;;
  esac

  # ---- Go-модули (#337). Идут БЕЗ СЕТИ: источник дат подменяется каталогом
  # фикстур. Гейт, который нельзя прогнать локально, проверяют только в CI —
  # то есть уже после отправки.
  go_repo="$(mktemp -d)"
  go_fx="$(mktemp -d)"
  (
    cd "$go_repo"
    git init -q -b main
    git config user.email t@t; git config user.name t
    printf 'module example.test/app\n\ngo 1.26\n\nrequire (\n\texample.com/old v1.0.0\n)\n' > go.mod
    printf '{}' > package-lock.json
    mkdir -p frontend && cp package-lock.json frontend/
    git add -A && git commit -qm base
    printf 'module example.test/app\n\ngo 1.26\n\nrequire (\n\texample.com/old v1.2.0\n\texample.com/newcomer v0.1.0\n)\n' > go.mod
    git add -A && git commit -qm bump
  )
  # Даты кладём так, чтобы одна версия была свежей, другая выдержанной, а у
  # третьей даты не было вовсе — три разных ответа гейта.
  printf '2026-08-30T00:00:00Z' > "$go_fx/example.com_old@v1.2.0"
  # example.com_newcomer@v0.1.0 намеренно НЕ создаём: «возраст неизвестен»

  go_out="$(cd "$go_repo" && DEP_AGE_NOW=2026-09-01 GOPROXY_FIXTURES="$go_fx" \
    "$OLDPWD/scripts/gates/dep-age.sh" HEAD^ 2>&1)" && go_rc=0 || go_rc=1

  if [ "$go_rc" -eq 0 ]; then
    echo "✘ self-test: на молодом Go-модуле гейт промолчал"; fail=1
  fi
  case "$go_out" in
    *"example.com/old@v1.2.0"*) : ;;
    *) echo "✘ self-test: молодая Go-версия не названа"; fail=1 ;;
  esac
  case "$go_out" in
    *"возраст неизвестен"*) : ;;
    *) echo "✘ self-test: модуль без даты не отнесён к «возраст неизвестен»"; fail=1 ;;
  esac
  case "$go_out" in
    *"go.mod — 2"*) : ;;
    *) echo "✘ self-test: число новых Go-версий названо неверно"; fail=1 ;;
  esac
  # Обратная сторона: та же версия месяцем позже обязана пройти. Без этого
  # случая «всегда красный» тоже сошёл бы за работающий гейт.
  printf '2026-07-01T00:00:00Z' > "$go_fx/example.com_newcomer@v0.1.0"
  if ! (cd "$go_repo" && DEP_AGE_NOW=2026-10-01 GOPROXY_FIXTURES="$go_fx" \
      "$OLDPWD/scripts/gates/dep-age.sh" HEAD^ >/dev/null 2>&1); then
    echo "✘ self-test: выдержанные Go-версии всё ещё красные"; fail=1
  fi
  rm -rf "$go_repo" "$go_fx"

  if [ "$fail" -ne 0 ]; then exit 1; fi
  echo "✓ dep-age --self-test: на PR #194 падает и называет обе версии, неделей позже зелёный;"
  echo "  новые имена названы на ${names_fixture:0:7}, на обычном bump молчит, подсадка ловится;"
  echo "  Go-модули: молодой красит, без даты — «возраст неизвестен», выдержанный проходит"
  exit 0
fi

ref=""
if [ "${1:-}" = "--ref" ]; then
  ref="$2"
  shift 2
fi

base="${1:-}"
if [ -z "$base" ]; then
  base="$(git merge-base origin/main HEAD)"
fi

# Порог спрашивается у npm из папки фронта: там лежит .npmrc проекта, и на
# машине без личного конфига (то есть в CI) только он и отвечает. Запуск из
# корня давал бы «не задан» и молча подставлял умолчание — то же правило
# двумя числами.
threshold="$(cd frontend && npm config get min-release-age 2>/dev/null || true)"
if [ -z "$threshold" ] || [ "$threshold" = "null" ] || [ "$threshold" = "undefined" ]; then
  threshold="${DEP_AGE_THRESHOLD:-7}"
  echo "ℹ min-release-age не задан ни в frontend/.npmrc, ни в конфиге npm — беру ${threshold} дн. из умолчания гейта"
fi

if ! git cat-file -e "$base:$lock" 2>/dev/null; then
  echo "ℹ ${lock} в базе ${base:0:8} отсутствует — сравнивать не с чем, проверка не проводилась"
  exit 0
fi

# Что ветка добавляет в лок: пара «пакет → версия», которой в базе не было.
# Смотрим именно лок, а не package.json: диапазон `^8.2.0` в package.json
# молчит о том, какая версия на самом деле встанет.
added="$(node --input-type=module -e '
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

const [base, lock, ref] = process.argv.slice(1);
const fromGit = (rev) => execFileSync("git", ["show", `${rev}:${lock}`], { encoding: "utf8", maxBuffer: 1 << 28 });
const read = (src) => JSON.parse(src);
const before = read(fromGit(base));
const after = read(ref ? fromGit(ref) : readFileSync(lock, "utf8"));

// Ключ в `packages` — путь установки; имя пакета берём из последнего
// node_modules в нём, иначе scoped-пакеты теряют свою половину.
const nameOf = (path) => {
  const at = path.lastIndexOf("node_modules/");
  return at === -1 ? null : path.slice(at + "node_modules/".length);
};

const versions = (doc) => {
  const out = new Map();
  for (const [path, meta] of Object.entries(doc.packages ?? {})) {
    const name = nameOf(path);
    if (!name || !meta?.version) continue;
    out.set(`${name}@${meta.version}`, name);
  }
  return out;
};

// Имя, которого в проекте не было никогда, — это не bump, и возраст про него
// ничего не говорит: подсадной пакет через неделю выглядит как выдержанный.
// Множество имён строится отдельно от множества пар, потому что у знакомого
// пакета новая версия появляется постоянно, а новое имя — событие другого рода.
const namesOf = (doc) => new Set(versions(doc).values());

const had = versions(before);
const had_names = namesOf(before);
for (const [key, name] of versions(after)) {
  if (had.has(key)) continue;
  const fresh_name = !had_names.has(name) ? "new-name" : "";
  console.log(`${name}\t${key.slice(name.length + 1)}\t${fresh_name}`);
}
' "$base" "$lock" "$ref" | sort -u)"

if [ -z "$added" ]; then
  echo "ℹ dep-age: новых версий в ${lock} ветка не добавляет"
fi

# ---------------------------------------------------------------- go.mod
#
# Порог возраста заводился против supply chain, а не против npm: пакет,
# опубликованный вчера, подозрителен в любой экосистеме. До #337 все три слоя
# защиты (cooldown, min-release-age, этот гейт) покрывали только npm, то есть
# МЕНЬШУЮ половину проекта — Go здесь основной язык. Возраст четырёх модулей
# 01.09.2026 пришлось проверять руками.
gomod="go.mod"
go_added=""
if git cat-file -e "$base:$gomod" 2>/dev/null; then
  # Сравниваются РАЗОБРАННЫЕ списки, а не текст файла: `go mod tidy` двигает
  # строки между блоками require и переносит // indirect, и текстовый diff
  # объявил бы изменившимся то, что не менялось.
  # Разбор на python, а не awk: первая редакция была на awk и молча вернула
  # пусто на коммите, который go.mod ТОЧНО менял — то есть гейт ответил
  # «проверять нечего» там, где проверять было что.
  go_added="$(
    {
      git show "$base:$gomod"
      echo "---SPLIT---"
      if [ -n "$ref" ]; then git show "$ref:$gomod"; else cat "$gomod"; fi
    } | python3 -c '
import re, sys

raw = sys.stdin.read().split("---SPLIT---")
line_re = re.compile(r"^\s*(?:require\s+)?([^\s/]+\.[^\s]*)\s+(v[0-9][^\s]*)")

def parse(text):
    pairs, names = set(), set()
    for line in text.splitlines():
        line = line.split("//")[0]
        m = line_re.match(line)
        if not m:
            continue
        mod, ver = m.group(1), m.group(2)
        pairs.add((mod, ver))
        names.add(mod)
    return pairs, names

before, before_names = parse(raw[0])
after, _ = parse(raw[1] if len(raw) > 1 else "")
for mod, ver in sorted(after - before):
    fresh = "" if mod in before_names else "new-name"
    print("%s\t%s\t%s" % (mod, ver, fresh))
'
  )"
fi

if [ -z "$added" ] && [ -z "$go_added" ]; then
  echo "✓ dep-age: ни ${lock}, ни ${gomod} ветка не меняет — проверять нечего"
  echo "  не проверено: версии, уже лежавшие в локе и в go.mod до этой ветки;"
  echo "  экосистемы docker и github-actions (их держит cooldown в dependabot.yml)."
  exit 0
fi

now_epoch="$(date -u +%s)"
if [ -n "${DEP_AGE_NOW:-}" ]; then
  now_epoch="$(node -e 'process.stdout.write(String(Math.floor(Date.parse(process.argv[1] + "T00:00:00Z") / 1000)))' "$DEP_AGE_NOW")"
fi

checked=0
fresh=""
unknown=""
new_names=""
new_names_count=0
while IFS=$'\t' read -r name version kind; do
  [ -z "$name" ] && continue
  checked=$((checked + 1))
  if [ "$kind" = "new-name" ]; then
    new_names="${new_names}${new_names:+, }${name}"
    new_names_count=$((new_names_count + 1))
  fi
  published=""
  if ! published="$(npm view "$name" "time[$version]" 2>/dev/null)"; then
    published=""
  fi
  published="$(printf '%s' "$published" | tr -d '[:space:]')"
  if [ -z "$published" ]; then
    unknown="${unknown}    ${name}@${version} — registry не назвал дату публикации"$'\n'
    continue
  fi
  age_days="$(node -e '
    const [published, now] = process.argv.slice(1);
    process.stdout.write(String(Math.floor((Number(now) * 1000 - Date.parse(published)) / 86400000)));
  ' "$published" "$now_epoch")"
  if [ "$age_days" -lt "$threshold" ]; then
    fresh="${fresh}    ${name}@${version} — ${age_days} дн. (опубликована ${published%T*})"$'\n'
  fi
done <<< "$added"

# Возраст Go-модуля спрашивается у proxy.golang.org: он единственный источник,
# который знает дату публикации версии, не выкачивая сам модуль.
#
# Источник подменяется переменной, чтобы самопроверка шла БЕЗ СЕТИ: гейт,
# который нельзя прогнать локально, проверяют только в CI — то есть уже после
# отправки. Тот же довод, что у ISSUE_FIXTURES в issue-close.sh.
go_checked=0
while IFS=$'\t' read -r name version kind; do
  [ -z "$name" ] && continue
  go_checked=$((go_checked + 1))
  if [ "$kind" = "new-name" ]; then
    new_names="${new_names}${new_names:+, }${name}"
    new_names_count=$((new_names_count + 1))
  fi
  published=""
  if [ -n "${GOPROXY_FIXTURES:-}" ]; then
    fixture="${GOPROXY_FIXTURES}/$(printf '%s' "${name}@${version}" | tr '/' '_')"
    [ -f "$fixture" ] && published="$(cat "$fixture")"
  else
    # Путь модуля в запросе экранируется: заглавная буква пишется как !буква,
    # иначе прокси не найдёт модуль с заглавными в имени.
    escaped="$(printf '%s' "$name" | sed -E 's/([A-Z])/!\l\1/g')"
    published="$(curl -sS --max-time 20 "https://proxy.golang.org/${escaped}/@v/${version}.info" 2>/dev/null \
      | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{process.stdout.write(JSON.parse(d).Time||"")}catch{}})' || true)"
  fi
  published="$(printf '%s' "$published" | tr -d '[:space:]')"
  if [ -z "$published" ]; then
    unknown="${unknown}    ${name}@${version} — proxy.golang.org не назвал дату публикации"$'\n'
    continue
  fi
  age_days="$(node -e '
    const [published, now] = process.argv.slice(1);
    process.stdout.write(String(Math.floor((Number(now) * 1000 - Date.parse(published)) / 86400000)));
  ' "$published" "$now_epoch")"
  if [ "$age_days" -lt "$threshold" ]; then
    fresh="${fresh}    ${name}@${version} — ${age_days} дн. (опубликована ${published%T*})"$'\n'
  fi
done <<< "$go_added"

# Две экосистемы называются ОТДЕЛЬНО, а не одним числом: молчание про одну из
# них снаружи неотличимо от «там чисто», и до #337 так и было.
echo "dep-age: npm — новых версий ${checked}; go.mod — ${go_checked}; порог ${threshold} дн."
if [ "$new_names_count" -gt 0 ]; then
  echo "  из них имён, которых в проекте не было: ${new_names_count} — ${new_names}"
  echo "  возраст про новое имя ничего не говорит: посмотрите на них глазами"
fi

status=0
if [ -n "$fresh" ]; then
  echo "✘ моложе карантина:"
  printf '%s' "$fresh"
  status=1
fi
if [ -n "$unknown" ]; then
  echo "✘ возраст неизвестен (незнание — не «всё в порядке»):"
  printf '%s' "$unknown"
  status=1
fi

# Правило 11: инструмент обязан называть, чего он НЕ проверил.
echo "  не проверено: версии, уже лежавшие в локе и в go.mod до этой ветки;"
echo "  экосистемы docker и github-actions (их держит cooldown в dependabot.yml);"
echo "  security-обновления — их cooldown намеренно не тормозит, и это осознанно:"
echo "  01.09 CRITICAL закрывался версией, которую порог мог бы не пустить;"
echo "  существование пакета до того, как его имя появилось в ответе модели."

if [ "$status" -eq 0 ]; then
  echo "✓ dep-age: все новые версии старше ${threshold} дн."
fi
exit "$status"
