#!/usr/bin/env bash
#
# Данные владельца не должны уезжать в публичный репозиторий.
#
# Репозиторий публичный. Суммы, остатки, id транзакций, имена личных счетов и
# объёмы личной книги не имеют отношения к продукту. Правило записано в мае и
# всё равно было нарушено рукой — поэтому оно перестало быть правилом и стало
# проверкой.
#
# ⚠️ ЧТО ИМЕННО ПРОВЕРЯЕТСЯ — сказано здесь, потому что раньше обещание и
# поведение расходились, и это стоило ложного срабатывания (issue #331).
# Проверяется ДЕЛЬТА ветки: `git diff <база>...HEAD` (три точки, то есть от
# merge-base) плюс сообщения коммитов ветки. Рабочее дерево целиком НЕ
# проверяется ни одним из трёх классов. Строка, пришедшая чужим PR и лежащая
# в main, здесь не находится по конструкции — её убирают отдельной работой,
# а не сообщением о чужой находке при каждом push.
#
# ⚠️ ЧЕГО НЕ ВИДИТ, чтобы не додумывали:
#   • значения, которых в текущем ledger уже нет (старый остаток);
#   • суммы меньше тысячи и круглые сотни — неотличимы от фикстуры;
#   • счётчики меньше двадцати — там же;
#   • всё, что уже запушено: историю отсюда не переписывают;
#   • суммы без копеек, числа словами, скриншоты и вложения.
#
# Маркеры читаются из живого ledger в момент вызова и здесь не хранятся:
# список чужих остатков, закоммиченный в публичный гейт, был бы той самой
# утечкой, от которой он защищает.
#
# Использование:
#   scripts/gates/owner-data.sh              проверить текущую ветку
#   scripts/gates/owner-data.sh --self-test  прогон на подсадках, без сети
#
# Переменные:
#   KB_LEDGER=FILE   журнал трат (умолчание — рабочее место владельца)
#   KB_BOOK=FILE     книга xlsx: оттуда дочитываются счета БЕЗ транзакций
#   DATA_BASE=REF    база сравнения (умолчание origin/main; self-test ставит main)
set -euo pipefail

DATA_BASE="${DATA_BASE:-origin/main}"

# ---------------------------------------------------------------- маркеры
#
# Класс 1: суммы от тысячи, не кратные сотне, плюс каждый id и три итога.
#
# Обе отсечки существуют потому, что первая редакция кричала волком: ниже
# тысячи число неотличимо от фикстуры, а круглая сумма в тесте совпадала с
# настоящей тратой того же размера. Гейт, срабатывающий на обычных тестовых
# данных, выключают за неделю.
value_marks() {
  python3 - "$1" <<'PY'
import json, re, sys
from decimal import Decimal

out, total = set(), {"expense": Decimal(0), "income": Decimal(0)}
for line in open(sys.argv[1], encoding="utf-8"):
    if not line.strip():
        continue
    r = json.loads(line)
    a = Decimal(r["amount"])
    total[r["kind"]] = total.get(r["kind"], Decimal(0)) + a
    # Круглые сотни типовы в фикстурах — их пишут не глядя. Замер 27.08: 18 %
    # маркеров кратны сотне, и один такой дал ложную тревогу на публичном репо.
    if a >= 1000 and a % 100 != 0:
        out.add(f"{a:.2f}")
    out.add(r["id"])
for v in total.values():
    out.add(f"{v:.2f}")
out.add(f"{total['income'] - total['expense']:.2f}")

# Сумма утекает в том виде, в каком её читает человек, а не в каком она
# хранится. Движок печатает деньги с разделёнными разрядами, поэтому каждый
# маркер разворачивается во все написания одного числа: любой десятичный
# разделитель, с группировкой и без, обычным пробелом и неразрывным, и без
# знака (проза говорит «расходы 12 345,67», а не «-12 345,67»).
#
# Каждое из этих написаний было дырой. Первая редакция сравнивала только
# хранимую строку и пропускала все три итога. Вторая разделяла разряды, но
# оставляла исходный разделитель — искала точку там, где утечка несёт запятую.
# Оба итога в ledger отрицательные, поэтому шаблон, привязанный к цифре, не
# совпадал с ними вовсе. Все три нашлись подсадкой настоящего итога, а не
# чтением кода.
forms = set()
for m in (x for x in out if x and x != "0.00"):
    forms.add(m)
    mm = re.fullmatch(r"-?(\d+)[.,](\d{2})", m)
    if not mm:
        continue  # id транзакции: разворачивать нечего
    whole, frac = mm.groups()
    groupings = [whole]
    if len(whole) > 3:
        parts = [whole[max(0, k - 3):k] for k in range(len(whole), 0, -3)][::-1]
        groupings += [" ".join(parts), " ".join(parts)]
    for g in groupings:
        for sep in (".", ","):
            forms.add(f"{g}{sep}{frac}")
for m in sorted(forms):
    print(m)
PY
}

# Класс 2: не суммы, а СТРУКТУРА личных финансов — имена счетов и объёмы книги.
#
# Заведён 06.08.2026 после третьего случая утечки за неделю. Первые два класса
# к тому моменту уже ловились, и гейт честно молчал: утекло то, чего он не умел
# видеть. «Займ → Коллеге» не сумма и не id, но говорит о человеке больше, чем
# любая из них.
#
# Бренды банков пропускаем намеренно: правило владельца разрешает их в
# фикстурах, и гейт, ругающийся на слово «Сбербанк» в тесте, будет выключен за
# неделю. Отбираем счета с родом — стрелка в имени и есть личная структура.
structure_marks() {
  python3 - "$1" <<'PY'
import json, sys

accounts, counts = set(), {}
rows = [json.loads(l) for l in open(sys.argv[1], encoding="utf-8") if l.strip()]
for r in rows:
    a = (r.get("account") or "").strip()
    if "→" in a:  # род → элемент: личная структура, не бренд
        accounts.add(a)
    counts[r["kind"]] = counts.get(r["kind"], 0) + 1
for a in sorted(accounts):
    print("ACC\t" + a)
sizes = {len(rows), *counts.values()}
for c in sorted(x for x in sizes if x >= 20):  # ниже двадцати неотличимо от фикстуры
    print("CNT\t" + str(c))
PY
}

# ------------------------------------------------------------------ дельта
#
# Одно место, где решается, ЧТО считается изменением. Раньше ответ был разным у
# соседних проверок в одном блоке: одна сравнивала добавленные строки, другая
# брала имена файлов из дельты и грепала эти файлы С ДИСКА целиком. Вторая и
# останавливала push на чужой строке из давнего PR (#331).
added_lines() {
  # Путь передаётся отдельными словами, а не подстановкой в строку: `*.md`
  # без кавычек раскрылся бы глоббингом рабочего каталога, и проверка пошла бы
  # по случайным файлам вместо дельты.
  if [ "$#" -gt 0 ]; then
    git diff --unified=0 "$DATA_BASE...HEAD" -- "$@" 2>/dev/null \
      | grep '^+' | grep -v '^+++' || true
  else
    git diff --unified=0 "$DATA_BASE...HEAD" 2>/dev/null \
      | grep '^+' | grep -v '^+++' || true
  fi
}

commit_messages() {
  git log "$DATA_BASE..HEAD" --format=%B 2>/dev/null || true
}

# ------------------------------------------------------------------- гейт
check_owner_data() {
  local ledger="${KB_LEDGER:-$HOME/claude-cowork/finances/transactions.jsonl}"
  local book="${KB_BOOK:-$HOME/claude-cowork/finances/Учёт_финансов.xlsx}"
  local failed=0

  # База обязана существовать. Раньше её отсутствие уводило в несуществующую
  # ветку else — проверка не выполнялась и выглядела пройденной. Проверка,
  # которая не смогла выполниться, и проверка, которая разрешила, снаружи
  # одинаковы; поэтому здесь говорится вслух (форма 9 Правила 11).
  if ! git rev-parse --verify --quiet "$DATA_BASE" >/dev/null; then
    echo "⚠ базы сравнения $DATA_BASE нет — данные владельца НЕ проверены"
    echo "  fix: git fetch origin   (или DATA_BASE=<ref>)"
    return 1
  fi

  local delta msgs
  delta="$(added_lines)"
  msgs="$(commit_messages)"

  # ---- 1. значения: суммы, id, итоги
  if [ ! -f "$ledger" ]; then
    echo "⚠ ledger не найден ($ledger) — суммы и имена счетов НЕ проверены (KB_LEDGER)"
  else
    local marks hits mhits
    marks="$(mktemp)"
    value_marks "$ledger" > "$marks"
    hits="$(printf '%s\n' "$delta" | grep -F -f "$marks" | head -5 || true)"
    mhits="$(printf '%s\n' "$msgs" | grep -F -f "$marks" | head -5 || true)"
    rm -f "$marks"
    if [ -n "$hits$mhits" ]; then
      echo "✘ в изменениях есть числа из живого ledger — репозиторий публичный"
      [ -n "$hits" ] && { echo "  в коде:"; printf '%s\n' "$hits" | sed 's/^/    /'; }
      [ -n "$mhits" ] && { echo "  в сообщениях коммитов:"; printf '%s\n' "$mhits" | sed 's/^/    /'; }
      echo "  Замените выдуманными значениями."
      failed=1
    fi

    # ---- 2. структура: имена счетов и объёмы книги
    local sm acc_pat cnt_pat
    sm="$(mktemp)"
    structure_marks "$ledger" > "$sm"
    acc_pat="$(awk -F'\t' '$1=="ACC"{print $2}' "$sm")"
    cnt_pat="$(awk -F'\t' '$1=="CNT"{print $2}' "$sm")"
    rm -f "$sm"

    # Дочитываем имена с листа «Счета»: там живут счета без транзакций, которых
    # в журнале нет по конструкции — именно так устроен долговой счёт (выдача
    # меняет баланс, а не журнал). Цена этой дыры замерена: такое имя стояло в
    # публичном репозитории 17 раз, гейт молчал. Спрашиваем ДВИЖОК, а не
    # открываем книгу своим парсером: он единственный писатель и читает лист
    # без потери строк (режим read_only у openpyxl их терял, замер 04.08).
    #
    # ⚠️ Код возврата проверяется отдельно от вывода. Установленный бинарь может
    # быть старее команды: тогда он пишет «unknown fin subcommand» и выходит с 2,
    # а `2>/dev/null` превратил бы это в пустой список — непроведённая проверка
    # выглядела бы как проведённая и чистая.
    if [ ! -f "$book" ] || ! command -v kbengine >/dev/null 2>&1; then
      echo "⚠ книга не прочитана — счета БЕЗ транзакций в проверку НЕ вошли ($book)"
    else
      local book_out from_book
      if book_out="$(kbengine fin accounts --from "$book" 2>&1)"; then
        from_book="$(printf '%s' "$book_out" | grep '→' || true)"
        [ -n "$from_book" ] && acc_pat="$(printf '%s\n%s' "$acc_pat" "$from_book" \
          | grep -v '^$' | sort -u)"
      else
        echo "⚠ kbengine fin accounts отказал — счета БЕЗ транзакций в проверку НЕ вошли"
        echo "  причина: $(printf '%s' "$book_out" | head -1)"
      fi
    fi

    local struct_hits="" prose_hits=""
    if [ -n "$acc_pat" ]; then
      struct_hits="$(printf '%s\n' "$delta" | grep -F -f <(printf '%s\n' "$acc_pat") | head -5 || true)"
    fi
    # Счётчики — только в прозе и только рядом со словом о строках или записях.
    # Замер 06.08 показал, почему шире нельзя: числа 22 и 77 живут в
    # package-lock.json, в SVG и в хешах go.sum, и гейт на голых числах кричал
    # бы волком в каждом push.
    #
    # ⚠️ Ищем в ДОБАВЛЕННЫХ строках прозы, а не в файлах прозы. До #331 здесь
    # брались имена тронутых *.md и грепался файл целиком с диска: любая правка
    # CHANGELOG заставляла гейт найти чужую строку из давнего PR. Разработчик
    # учится жать DATA_SKIP=1 на находке, которую не создавал, — и прожмёт его
    # на настоящей.
    if [ -n "$cnt_pat" ]; then
      local nums word md_delta
      nums="$(printf '%s' "$cnt_pat" | paste -sd'|' -)"
      word='строк|строки|строках|записей|операций'
      md_delta="$(added_lines '*.md')"
      [ -n "$md_delta" ] && prose_hits="$(printf '%s\n' "$md_delta" | grep -nE \
        "($word)[^0-9]{0,20}($nums)([^0-9]|\$)|($nums)[^0-9]{0,20}($word)" \
        | head -5 || true)"
    fi

    if [ -n "$struct_hits$prose_hits" ]; then
      echo "✘ в изменениях есть СТРУКТУРА личных финансов — репозиторий публичный"
      [ -n "$struct_hits" ] && { echo "  имена счетов владельца:"; printf '%s\n' "$struct_hits" | sed 's/^/    /'; }
      [ -n "$prose_hits" ] && { echo "  объёмы личной книги в прозе:"; printf '%s\n' "$prose_hits" | sed 's/^/    /'; }
      echo "  Имена счетов в фикстурах — выдуманные (род → элемент сохранить)."
      echo "  Объёмы книги в CHANGELOG не нужны вовсе: пишите качественно, а не числом."
      failed=1
    fi
  fi

  # ---- 3. правило про КЛАСС, а не про совпадение со списком
  #
  # Всё выше сравнивает изменения со СПИСКОМ значений из живого ledger. У такого
  # критерия два врождённых отказа, и оба уже случались:
  #
  #   1. Нет ledger — нет списка — проверка пропускается. На CI ledger'а нет
  #      никогда, значит там этот класс не проверялся ни разу, а строка
  #      «owner-data check skipped» читалась как «чисто». Security default обязан
  #      быть deny, а был allow.
  #   2. Производные не перечислимы. Список знает суммы строк и три итога;
  #      подытог по счёту, средний чек, число дней подряд — каждый новый агрегат
  #      это новый факт вне списка. Утечка 06.08.2026 прошла ровно так.
  #
  # Поэтому здесь запрещена ФОРМА, а не значение: денежный литерал в журнале
  # рядом со словом об остатке, счёте или итоге. Работает без ledger — то есть
  # и на CI — и ловит производные, которых в списке нет по определению.
  local money_hits="" changelog_delta
  changelog_delta="$(added_lines CHANGELOG.md)"
  if [ -n "$changelog_delta" ]; then
    money_hits="$(printf '%s\n' "$changelog_delta" \
      | grep -nE '(остат|баланс|счёт|счет|итог|потрачено|получено)[^0-9]{0,40}[0-9]{3,}[.,][0-9]{2}|[0-9]{3,}[.,][0-9]{2}[^0-9]{0,40}(остат|баланс|итог|на счёте|на счете)' \
      | head -5 || true)"
  fi
  if [ -n "$money_hits" ]; then
    echo "✘ денежный литерал в CHANGELOG рядом со словом об остатке или итоге"
    printf '%s\n' "$money_hits" | sed 's/^/    /'
    echo "  Журнал описывает, ЧТО чинилось, а не сколько денег у владельца."
    echo "  Если это выдуманное число в примере — уберите копейки или округлите."
    failed=1
  fi

  [ "$failed" -eq 0 ] || echo "  Осознанно: DATA_SKIP=1 git push"
  return "$failed"
}

# -------------------------------------------------------------- self-test
#
# Гейт без отрицательного контроля проверяют только в бою, то есть уже после
# отправки. Здесь он прогоняется на подсаженных утечках в одноразовом
# репозитории; живые данные владельца не читаются ни в одном случае.
#
# Случай 7 — тот самый, ради которого заведена #331: чужая строка в том же
# файле, но ВНЕ дельты, обязана молчать. Проверять его надо именно вместе с
# случаем 6 (та же строка в дельте обязана ловиться), иначе «починка» в виде
# выключенной проверки прошла бы самопроверку.
self_test() {
  local fails=0 cases=0
  local root; root="$(mktemp -d)"
  trap 'rm -rf "$root"' RETURN

  # Выдуманный ledger. Ни одно значение не взято из живых данных.
  local ledger="$root/ledger.jsonl"
  {
    for i in $(seq 1 40); do
      printf '{"id":"01FAKE%08d","kind":"expense","amount":"%d.77","account":"Банк"}\n' "$i" "$((1200 + i))"
    done
    for i in $(seq 41 65); do
      printf '{"id":"01FAKE%08d","kind":"income","amount":"%d.00","account":"Заначка → Тумбочка"}\n' "$i" "$((3000 + i))"
    done
  } > "$ledger"

  # Одноразовый репозиторий: main с историей, ветка с подсадкой.
  local repo="$root/repo"
  mkdir -p "$repo"
  (
    cd "$repo"
    git init -q -b main
    git config user.email t@t; git config user.name t
    printf 'предыстория\nв книге лежат 65 строк, и это чужая давняя строка\n' > CHANGELOG.md
    printf 'package main\n' > main.go
    git add -A && git commit -qm "base"
  )

  # run <описание> <ожидание: catch|silent> <тело подсадки>
  run() {
    local what="$1" expect="$2" body="$3"
    cases=$((cases + 1))
    (
      cd "$repo"
      git checkout -q -B probe main
      eval "$body"
      git add -A 2>/dev/null || true
      git -c user.email=t@t -c user.name=t commit -q --allow-empty -m "${PROBE_MSG:-probe}"
    )
    local out rc
    out="$(cd "$repo" && DATA_BASE=main KB_LEDGER="$ledger" KB_BOOK=/nonexistent \
      check_owner_data 2>&1)" && rc=0 || rc=1
    unset PROBE_MSG
    if [ "$expect" = catch ] && [ "$rc" -ne 1 ]; then
      printf '✘ %s: обязан был поймать, промолчал\n' "$what"; fails=$((fails + 1))
    elif [ "$expect" = silent ] && [ "$rc" -ne 0 ]; then
      printf '✘ %s: обязан был промолчать, поймал:\n%s\n' "$what" "$(printf '%s\n' "$out" | sed 's/^/    /')"
      fails=$((fails + 1))
    fi
  }

  # Итог берётся ИЗ фикстуры, а не вписывается на глаз: вписанное руками число
  # перестало бы быть итогом при первой же правке фикстуры, и случай зеленел бы,
  # ничего не проверяя. Пишется в том виде, в каком его печатает движок —
  # разряды через пробел, копейки через запятую.
  local exp_total
  exp_total="$(python3 - "$ledger" <<'PY'
import json, sys
from decimal import Decimal
t = sum((Decimal(json.loads(l)["amount"]) for l in open(sys.argv[1], encoding="utf-8")
         if l.strip() and json.loads(l)["kind"] == "expense"), Decimal(0))
whole, frac = f"{t:.2f}".split(".")
parts = [whole[max(0, k - 3):k] for k in range(len(whole), 0, -3)][::-1]
print(f"{' '.join(parts)},{frac}")
PY
)"

  run "1. сумма из ledger в коде"        catch 'printf "const x = 1207.77\n" >> main.go'
  run "2. id транзакции в коде"          catch 'printf "// 01FAKE00000005\n" >> main.go'
  run "3. итог с разрядами и запятой"    catch "printf '// расход $exp_total\n' >> main.go"
  run "4. сумма в сообщении коммита"     catch 'PROBE_MSG="fix: было 1207.77"; printf "x\n" >> main.go'
  run "5. имя личного счёта в фикстуре"  catch 'printf "// Заначка → Тумбочка\n" >> main.go'
  run "6. объём книги в прозе В ДЕЛЬТЕ"  catch 'printf "в книге 65 строк\n" >> CHANGELOG.md'
  run "7. чужая строка ВНЕ дельты"       silent 'printf "%s\n" "- fix: обычная запись" >> CHANGELOG.md'
  run "8. остаток с копейками в CHANGELOG" catch 'printf "остаток 9432.10\n" >> CHANGELOG.md'
  run "9. чистая ветка"                  silent 'printf "// ordinary change\n" >> main.go'

  # 10. отсутствие базы обязано ЗАКРЫВАТЬ, а не открывать.
  cases=$((cases + 1))
  if (cd "$repo" && DATA_BASE=refs/heads/nonexistent KB_LEDGER="$ledger" \
      KB_BOOK=/nonexistent check_owner_data >/dev/null 2>&1); then
    printf '✘ 10. нет базы сравнения: обязан отказать, разрешил\n'; fails=$((fails + 1))
  fi

  # 11. отсутствие ledger обязано быть НАЗВАНО вслух, а не выглядеть чистым.
  cases=$((cases + 1))
  local out11
  out11="$(cd "$repo" && git checkout -q -B probe main \
    && DATA_BASE=main KB_LEDGER=/nonexistent KB_BOOK=/nonexistent check_owner_data 2>&1 || true)"
  if ! printf '%s' "$out11" | grep -q 'ledger не найден'; then
    printf '✘ 11. нет ledger: обязан сказать вслух, промолчал\n'; fails=$((fails + 1))
  fi

  if [ "$fails" -eq 0 ]; then
    printf '✓ owner-data --self-test: %d из %d\n' "$cases" "$cases"
    return 0
  fi
  printf '✘ owner-data --self-test: провалено %d из %d\n' "$fails" "$cases"
  return 1
}

case "${1:-}" in
  --self-test) self_test; exit $? ;;
  "") check_owner_data; exit $? ;;
  *) echo "owner-data: неизвестный аргумент '$1' (пусто или --self-test)" >&2; exit 2 ;;
esac
