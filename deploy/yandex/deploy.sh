#!/usr/bin/env bash
# Выкатывает kbengine serve --tenants в Yandex Cloud: Serverless Container
# перед приватным бакетом с базами людей. Повторный запуск создает
# недостающее, приводит защиту существующего к описанию и выкатывает новую
# ревизию из текущего коммита.
#
#   BUCKET=<бакет> deploy/yandex/deploy.sh            выкатить
#   BUCKET=<бакет> deploy/yandex/deploy.sh --verify   только проверить живое
#
# FOLDER — каталог облака (по умолчанию kb-engine), YC — путь к yc.
# PROBE_TOKEN_FILE — файл 0600 со своим токеном: --verify тогда проверяет и
# вход (200 по X-KB-Token), а не только отказы.
# Устройство и почему оно такое: deploy/yandex/README.md.
#
# ponytail: идемпотентность написана руками (idof, bind, приведение свойств
# ниже) — потолок: каждое новое свойство ресурса чинится отдельной строкой.
# Путь апгрейда — модуль tofu с провайдером yandex рядом с deploy/terraform,
# и --verify станет `tofu plan` с пустым диффом.
set -euo pipefail

YC="${YC:-yc}"
FOLDER="${FOLDER:-kb-engine}"
BUCKET="${BUCKET:?BUCKET: имя бакета с базами людей}"
TZ_NAME="${TZ_NAME:-Asia/Yekaterinburg}"
registry=kb-engine sa=kb-server key=kb-bucket container=kb-server
say() { printf '%s\n' "$*" >&2; }
die() { say "✘ $*"; exit 1; }
yc_path="$(command -v "$YC")" || die "yc не найден: YC=$YC"
# docker берет вход в реестр у docker-credential-yc, а он лежит рядом с yc,
# который часто не в PATH (~/yandex-cloud/bin).
PATH="$(dirname "$yc_path"):$PATH"
cd "$(git rev-parse --show-toplevel)"

y() { "$YC" --folder-name "$FOLDER" "$@"; }
# idof prints the id of an existing resource, nothing when there is none.
# stdout и stderr раздельно: предупреждение yc в stderr не ломает JSON. Пустым
# считается только «… not found» от облака; любой другой отказ (вход в yc
# истек, нет прав) останавливает, а не ведет создавать ресурс заново.
idof() {
  local out err rc=0
  err="$(mktemp)"
  out="$(y "$@" --format json 2>"$err")" || rc=$?
  if [[ $rc == 0 ]]; then
    rm -f "$err"
    jq -r '.id // empty' <<<"$out"
  elif grep -qE 'with (name|id) .* not found' "$err"; then
    rm -f "$err"
  else
    cat "$err" >&2
    rm -f "$err"
    return 1
  fi
}

# bind grants role on a resource to the service account once: add-access-binding
# на уже выданную роль отвечает ошибкой, а не тишиной.
bind() { # <yc group…> <resource id> <role> <sa id>
  local id="${*: -3:1}" role="${*: -2:1}" sid="${*: -1}" group=("${@:1:$#-3}")
  if y "${group[@]}" list-access-bindings --id "$id" --format json |
    jq -e --arg r "$role" --arg s "$sid" 'any(.[]?; .role_id == $r and .subject.id == $s)' >/dev/null; then
    return
  fi
  y "${group[@]}" add-access-binding --id "$id" --role "$role" --service-account-id "$sid" >/dev/null
}

# verify checks the live state. Каждое чтение здесь может упасть (нет сети,
# нет ресурса) — тогда проверка печатает ✘, а не обрывает скрипт молча.
# Проверки идут через check "<что>" '<выражение>': выражение в одинарных
# кавычках вычисляется в check через eval, когда переменные уже заданы, —
# поэтому SC2016 (нет подстановки в кавычках) и SC2034 (keyj, body будто не
# используются) здесь ложные.
# shellcheck disable=SC2016,SC2034
verify() {
  local ok=1 sid kid bucket keyj url code body cloud broad objs unenc n
  sid="$(idof iam service-account get --name "$sa" || true)"
  kid="$(idof kms symmetric-key get --name "$key" || true)"
  bucket="$("$YC" storage bucket get "$BUCKET" --full --format json 2>/dev/null || echo '{}')"
  keyj="$(y kms symmetric-key get --name "$key" --format json 2>/dev/null || echo '{}')"
  check() { if eval "$2"; then say "✓ $1"; else say "✘ $1"; ok=0; fi; }
  check "сервисный аккаунт $sa есть" '[[ -n "$sid" ]]'
  # Пустой ответ читался бы как «все закрыто»: проверки бакета ниже требуют,
  # чтобы бакет был.
  check "бакет $BUCKET есть" '[[ "$(jq -r ".name // empty" <<<"$bucket")" == "$BUCKET" ]]'
  [[ "$(jq -r ".name // empty" <<<"$bucket")" == "$BUCKET" ]] || bucket='{"anonymous_access_flags":{"read":true},"acl":{"grants":[{"grantee_id":"?"}]}}'
  check "бакет шифруется ключом $key по умолчанию" '[[ -n "$kid" && "$(jq -r ".encryption.rules[0].kms_master_key_id // empty" <<<"$bucket")" == "$kid" ]]'
  check "у ключа защита от удаления (без ключа данные не прочесть)" '[[ "$(jq -r ".deletion_protection // false" <<<"$keyj")" == true ]]'
  check "у бакета версии (перезапись и удаление можно откатить)" '[[ "$(jq -r ".versioning // empty" <<<"$bucket")" == VERSIONING_ENABLED ]]'
  check "анонимный доступ к бакету закрыт" '[[ "$(jq -c "(.anonymous_access_flags // {}) | [.read, .list, .config_read] | map(. == true) | any" <<<"$bucket")" == false ]]'
  check "ACL бакета: только $sa, только чтение и запись" '[[ -n "$sid" && "$(jq -c --arg s "$sid" "[.acl.grants[]? | select(.grantee_id != \$s or (.permission | IN(\"PERMISSION_READ\", \"PERMISSION_WRITE\") | not))] | length" <<<"$bucket")" == 0 ]]'
  # Роли шире бакета (на каталог или облако) дали бы доступ к данным в обход
  # ACL. Владельцу облака они положены; сервисным аккаунтам — нет.
  cloud="$(y resource-manager folder get --name "$FOLDER" --format json 2>/dev/null | jq -r '.cloud_id // empty')"
  broad="$( { y resource-manager folder list-access-bindings --name "$FOLDER" --format json 2>/dev/null || echo '[]'; \
    [[ -n "$cloud" ]] && "$YC" resource-manager cloud list-access-bindings --id "$cloud" --format json 2>/dev/null || echo '[]'; } |
    jq -s -r '[.[][] | select(.subject.type == "serviceAccount" and (.role_id | test("^(storage\\.|kms\\.|editor$|admin$)")))] | length')"
  check "ни у одного сервисного аккаунта нет прав на данные шире бакета (было $broad)" '[[ "$broad" == 0 ]]'
  # Шифрование по умолчанию касается только новых объектов: все, что вне
  # test/ (там срок жизни день), обязано быть зашифровано этим ключом.
  objs="$("$YC" storage s3api list-objects --bucket "$BUCKET" --format json 2>/dev/null | jq -r '.contents[]?.key | select(startswith("test/") | not)' || true)"
  n=0 unenc=0
  while IFS= read -r k; do
    [[ -n "$k" ]] || continue
    n=$((n + 1))
    [[ "$("$YC" storage s3api head-object --bucket "$BUCKET" --key "$k" --format json 2>/dev/null | jq -r '.sse_kms_key_id // empty')" == "$kid" ]] || unenc=$((unenc + 1))
  done <<<"$objs"
  check "все $n объектов вне test/ зашифрованы ключом $key (не так: $unenc)" '[[ "$unenc" == 0 ]]'
  url="$(y serverless container get --name "$container" --format json 2>/dev/null | jq -r '.url // empty' || true)"
  check "контейнер есть и отвечает по HTTPS" '[[ "$url" == https://* ]]'
  if [[ "$url" == https://* ]]; then
    # Отказ должен прийти от приложения (realm kbengine), а не от платформы:
    # 401 и 403 облака выглядят иначе.
    body="$(curl -s -i --max-time 30 "${url%/}/api/engine" || true)"
    check "без токена — 401 от kbengine" 'grep -q "^HTTP/[0-9.]* 401" <<<"$body" && grep -qi "realm=\"kbengine\"" <<<"$body"'
    # X-KB-Token, как его шлют окно и kbengine-mcp: Authorization платформа
    # вырезает и проверяет как IAM.
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -H "X-KB-Token: $(openssl rand -hex 24)" "${url%/}/api/engine" || true)"
    check "с чужим токеном — 401 (было $code)" '[[ "$code" == 401 ]]'
    if [[ -n "${PROBE_TOKEN_FILE:-}" ]]; then
      code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -H "X-KB-Token: $(<"$PROBE_TOKEN_FILE")" "${url%/}/api/engine" || true)"
      check "со своим токеном — 200 (было $code)" '[[ "$code" == 200 ]]'
    else
      say "· вход своим токеном не проверен: PROBE_TOKEN_FILE не задан"
    fi
  fi
  [[ "$ok" == 1 ]] || die "проверка не прошла"
  say "адрес сервера: $url"
}

if [[ "${1:-}" == "--verify" ]]; then
  verify
  exit 0
fi

# Образ — ровно коммит: из дерева с правками или новыми файлами выкатилось бы
# то, чего нет в git (Dockerfile копирует весь контекст).
[[ -z "$(git status --porcelain)" ]] || die "в дереве незакоммиченные правки или новые файлы"
rev="$(git rev-parse --short=12 HEAD)"

say "· ключ KMS $key"
kid="$(idof kms symmetric-key get --name "$key")"
# Защита от удаления: удаленный ключ — это базы и финансы, которые нельзя
# расшифровать ничем. Ротация раз в год: каждая версия ключа — отдельная плата.
if [[ -z "$kid" ]]; then
  kid="$(y kms symmetric-key create --name "$key" --default-algorithm aes-256 \
    --rotation-period 8760h --deletion-protection --format json | jq -r .id)"
else
  y kms symmetric-key update --id "$kid" --deletion-protection >/dev/null
fi

say "· сервисный аккаунт $sa"
sid="$(idof iam service-account get --name "$sa")"
[[ -n "$sid" ]] || sid="$(y iam service-account create --name "$sa" \
  --description "kbengine serve --tenants: only bucket $BUCKET, key $key, registry $registry" --format json | jq -r .id)"

say "· бакет $BUCKET: шифрование, версии, доступ только $sa"
bind kms symmetric-key "$kid" kms.keys.encrypterDecrypter "$sid"
# ACL, а не роль на каталог: роль открыла бы и любой будущий бакет каталога.
# --grants заменяет ACL целиком — чужих записей в нем не остается. Версии:
# ошибка записи или украденный токен не стирают данные насовсем.
"$YC" storage bucket update --name "$BUCKET" --encryption key-id="$kid" \
  --versioning versioning-enabled \
  --public-read=false --public-list=false --public-config-read=false \
  --grants grant-type=grant-type-account,grantee-id="$sid",permission=permission-read \
  --grants grant-type=grant-type-account,grantee-id="$sid",permission=permission-write >/dev/null

say "· реестр $registry и образ $rev"
rid="$(idof container registry get --name "$registry")"
[[ -n "$rid" ]] || rid="$(y container registry create --name "$registry" --format json | jq -r .id)"
bind container registry "$rid" container-registry.images.puller "$sid"
"$YC" container registry configure-docker >/dev/null
meta="$(mktemp)"
trap 'rm -f "$meta"' EXIT
docker buildx build --platform linux/amd64 --build-arg VERSION="$rev" \
  -t "cr.yandex/$rid/kbengine:$rev" --push --metadata-file "$meta" .
digest="$(jq -r '."containerimage.digest"' "$meta")"
[[ "$digest" == sha256:* ]] || die "сборка не назвала digest образа"

say "· контейнер $container"
[[ -n "$(idof serverless container get --name "$container")" ]] ||
  y serverless container create --name "$container" >/dev/null
# Образ по digest, не по тегу: тег можно перевесить, digest — нет.
# Потолок экземпляров и запросов — потолок счета за чужие запросы. Лимиты
# --zone-* действуют на каждую зону доступности (в ru-central1 их три): 1
# экземпляр и 8 запросов на зону — не больше 3 экземпляров и 24 запросов.
# Метаданные: только GCE-точка, из нее код берет IAM-токен; AWS v1 закрыта.
# Секретов в окружении нет: доступ к бакету — через сервисный аккаунт.
# --command обязателен: --args платформа не дописывает к ENTRYPOINT образа,
# а запускает как команду («"serve": executable file not found»).
y serverless container revision deploy --container-name "$container" \
  --image "cr.yandex/$rid/kbengine@$digest" --service-account-id "$sid" \
  --memory 512MB --cores 1 --concurrency 8 --execution-timeout 30s \
  --zone-instances-limit 1 --zone-requests-limit 8 \
  --metadata-options aws-v1-http-endpoint=disabled,gce-http-endpoint=enabled \
  --environment TZ="$TZ_NAME" \
  --command /kbengine --args serve,--tenants,"s3://$BUCKET",--addr,0.0.0.0:8080,--allow-open >/dev/null
# Вызов открыт: окно и kbengine-mcp ходят по HTTPS, а вход — личный токен
# приложения (сервер хранит только SHA-256). Без токена — 401.
y serverless container allow-unauthenticated-invoke --name "$container" >/dev/null

verify
