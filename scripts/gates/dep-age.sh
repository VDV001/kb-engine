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
  if [ "$fail" -ne 0 ]; then exit 1; fi
  echo "✓ dep-age --self-test: на PR #194 падает и называет обе версии, неделей позже зелёный"
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

const had = versions(before);
for (const [key, name] of versions(after)) {
  if (!had.has(key)) console.log(`${name}\t${key.slice(name.length + 1)}`);
}
' "$base" "$lock" "$ref" | sort -u)"

if [ -z "$added" ]; then
  echo "✓ dep-age: новых версий в ${lock} ветка не добавляет — проверять нечего"
  exit 0
fi

now_epoch="$(date -u +%s)"
if [ -n "${DEP_AGE_NOW:-}" ]; then
  now_epoch="$(node -e 'process.stdout.write(String(Math.floor(Date.parse(process.argv[1] + "T00:00:00Z") / 1000)))' "$DEP_AGE_NOW")"
fi

checked=0
fresh=""
unknown=""
while IFS=$'\t' read -r name version; do
  [ -z "$name" ] && continue
  checked=$((checked + 1))
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

echo "dep-age: проверено новых версий — ${checked}, порог ${threshold} дн."

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
echo "  не проверено: версии, уже лежавшие в локе до этой ветки; экосистемы"
echo "  gomod, docker и github-actions (их держит cooldown в dependabot.yml);"
echo "  security-обновления — их cooldown намеренно не тормозит."

if [ "$status" -eq 0 ]; then
  echo "✓ dep-age: все новые версии старше ${threshold} дн."
fi
exit "$status"
