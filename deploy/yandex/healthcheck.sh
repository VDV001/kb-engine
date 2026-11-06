#!/usr/bin/env bash
# Проверка kb-сервера по таймеру с алертом в Telegram (решение владельца
# 06.11): функция kb-healthcheck раз в 5 минут зовет /healthz контейнера
# kb-server и при сбое пишет ботом владельца. Повторный запуск приводит все к
# описанию и выкатывает новую версию функции.
#
#   TG_CHAT=<id чата> deploy/yandex/healthcheck.sh            выкатить
#   TG_CHAT=<id чата> deploy/yandex/healthcheck.sh --verify   вызвать один раз
#
# Токен бота — в Lockbox (секрет kb-alert-bot, ключ token): его кладет
# владелец, скрипт его не видит и не печатает. FOLDER, YC — как в deploy.sh.
#
# ponytail: проверка идет из того же облака — общий сбой Yandex Cloud она не
# заметит. Внешнюю проверку держит UptimeRobot (монитор kb-сервер /healthz,
# письмо), поэтому слепое пятно у них разное.
set -euo pipefail

YC="${YC:-yc}"
FOLDER="${FOLDER:-kb-engine}"
TG_CHAT="${TG_CHAT:?TG_CHAT: id чата, куда слать алерты}"
secret=kb-alert-bot sa=kb-healthcheck fn=kb-healthcheck trigger=kb-healthcheck-5m container=kb-server
say() { printf '%s\n' "$*" >&2; }
die() { say "✘ $*"; exit 1; }
command -v "$YC" >/dev/null || die "yc не найден: YC=$YC"
cd "$(git rev-parse --show-toplevel)"
y() { "$YC" --folder-name "$FOLDER" "$@"; }

# id of an existing resource or nothing (как idof в deploy.sh: «не найден» —
# пусто, любой другой отказ останавливает).
idof() {
  local out err rc=0
  err="$(mktemp)"
  out="$(y "$@" --format json 2>"$err")" || rc=$?
  if [[ $rc == 0 ]]; then
    rm -f "$err"
    jq -r '.id // empty' <<<"$out"
  elif grep -qE 'not found' "$err"; then
    rm -f "$err"
  else
    cat "$err" >&2
    rm -f "$err"
    return 1
  fi
}

bind() { # <yc group…> <resource id> <role> <sa id>
  local id="${*: -3:1}" role="${*: -2:1}" sid="${*: -1}" group=("${@:1:$#-3}")
  if y "${group[@]}" list-access-bindings --id "$id" --format json |
    jq -e --arg r "$role" --arg s "$sid" 'any(.[]?; .role_id == $r and .subject.id == $s)' >/dev/null; then
    return
  fi
  y "${group[@]}" add-access-binding --id "$id" --role "$role" --service-account-id "$sid" >/dev/null
}

# verify invokes the function once: сервер жив — ok и тишина в Telegram.
verify() {
  local out
  out="$(echo '{}' | y serverless function invoke --name "$fn" --data-stdin 2>/dev/null || true)"
  say "ответ функции: $out"
  [[ "$(jq -r '.ok // false' <<<"$out")" == true ]] || die "функция не увидела живой сервер"
  [[ -n "$(idof serverless trigger get --name "$trigger" || true)" ]] || die "нет таймера $trigger"
  say "✓ функция видит живой сервер, таймер $trigger есть"
}

if [[ "${1:-}" == "--verify" ]]; then
  verify
  exit 0
fi

sec="$(idof lockbox secret get --name "$secret")"
[[ -n "$sec" ]] || die "нет секрета $secret: его создает владелец (токен бота, ключ token)"
url="$(y serverless container get --name "$container" --format json | jq -r '.url // empty')"
[[ "$url" == https://* ]] || die "нет адреса контейнера $container"

say "· сервисный аккаунт $sa: читать только секрет $secret"
sid="$(idof iam service-account get --name "$sa")"
[[ -n "$sid" ]] || sid="$(y iam service-account create --name "$sa" \
  --description "kb-healthcheck: only secret $secret" --format json | jq -r .id)"
bind lockbox secret "$sec" lockbox.payloadViewer "$sid"

say "· функция $fn"
fid="$(idof serverless function get --name "$fn")"
[[ -n "$fid" ]] || fid="$(y serverless function create --name "$fn" --format json | jq -r .id)"
# Пакет — только index.py: тесты в функцию не едут.
pkg="$(mktemp -d)"
trap 'rm -rf "$pkg"' EXIT
cp deploy/yandex/healthcheck/index.py "$pkg/"
y serverless function version create --function-name "$fn" --runtime python312 \
  --entrypoint index.handler --memory 128MB --execution-timeout 60s \
  --source-path "$pkg" --service-account-id "$sid" \
  --environment "HEALTH_URL=${url%/}/healthz,TG_CHAT=$TG_CHAT" \
  --secret environment-variable=TG_TOKEN,id="$sec",key=token >/dev/null
bind serverless function "$fid" functions.functionInvoker "$sid"

say "· таймер $trigger: каждые 5 минут"
[[ -n "$(idof serverless trigger get --name "$trigger")" ]] ||
  y serverless trigger create timer --name "$trigger" --cron-expression '0/5 * ? * * *' \
    --invoke-function-id "$fid" --invoke-function-service-account-id "$sid" >/dev/null

verify
