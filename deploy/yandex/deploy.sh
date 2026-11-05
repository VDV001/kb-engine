#!/usr/bin/env bash
# Выкатывает kbengine serve --tenants в Yandex Cloud: Serverless Container
# перед приватным бакетом с базами людей. Повторный запуск создает только
# недостающее и выкатывает новую ревизию из текущего коммита.
#
#   BUCKET=<бакет> deploy/yandex/deploy.sh            выкатить
#   BUCKET=<бакет> deploy/yandex/deploy.sh --verify   только проверить живое
#
# FOLDER — каталог облака (по умолчанию kb-engine), YC — путь к yc.
# Устройство и почему оно такое: deploy/yandex/README.md.
set -euo pipefail

YC="${YC:-yc}"
FOLDER="${FOLDER:-kb-engine}"
BUCKET="${BUCKET:?BUCKET: имя бакета с базами людей}"
TZ_NAME="${TZ_NAME:-Asia/Yekaterinburg}"
registry=kb-engine sa=kb-server key=kb-bucket container=kb-server
# docker берет вход в реестр у docker-credential-yc, а он лежит рядом с yc,
# который часто не в PATH (~/yandex-cloud/bin).
PATH="$(dirname "$(command -v "$YC")"):$PATH"
cd "$(git rev-parse --show-toplevel)"

y() { "$YC" --folder-name "$FOLDER" "$@"; }
# idof prints the id of an existing resource, nothing when there is none.
# Пустым считается только «not found»: любой другой отказ (вход в yc истек,
# нет прав) остановил бы скрипт, а не повел его создавать ресурс заново.
idof() {
  local out
  if out="$(y "$@" --format json 2>&1)"; then
    jq -r '.id // empty' <<<"$out"
  elif ! grep -q 'not found' <<<"$out"; then
    printf '%s\n' "$out" >&2
    return 1
  fi
}
say() { printf '%s\n' "$*" >&2; }
die() { say "✘ $*"; exit 1; }

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

verify() {
  local ok=1 sid kid bucket url code
  sid="$(idof iam service-account get --name "$sa")"
  kid="$(idof kms symmetric-key get --name "$key")"
  bucket="$("$YC" storage bucket get "$BUCKET" --full --format json)"
  check() { if eval "$2"; then say "✓ $1"; else say "✘ $1"; ok=0; fi; }
  check "бакет шифруется ключом $key" '[[ -n "$kid" && "$(jq -r ".encryption.rules[0].kms_master_key_id // empty" <<<"$bucket")" == "$kid" ]]'
  check "у ключа защита от удаления (без ключа данные не прочесть)" '[[ "$(y kms symmetric-key get --name "$key" --format json | jq -r .deletion_protection)" == true ]]'
  check "анонимный доступ к бакету закрыт" '[[ "$(jq -c ".anonymous_access_flags | [.read, .list, .config_read] | map(. == true) | any" <<<"$bucket")" == false ]]'
  check "ACL бакета: только $sa, только чтение и запись" '[[ "$(jq -c --arg s "$sid" "[.acl.grants[]? | select(.grantee_id != \$s or (.permission | IN(\"PERMISSION_READ\", \"PERMISSION_WRITE\") | not))] | length" <<<"$bucket")" == 0 ]]'
  url="$(y serverless container get --name "$container" --format json 2>/dev/null | jq -r '.url // empty')"
  check "контейнер есть и отвечает по HTTPS" '[[ "$url" == https://* ]]'
  if [[ "$url" == https://* ]]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "${url%/}/api/engine")"
    check "без токена — 401 (было $code)" '[[ "$code" == 401 ]]'
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -H "Authorization: Bearer $(openssl rand -hex 24)" "${url%/}/api/engine")"
    check "с чужим токеном — 401 (было $code)" '[[ "$code" == 401 ]]'
  fi
  [[ "$ok" == 1 ]] || die "проверка не прошла"
  say "адрес сервера: $url"
}

if [[ "${1:-}" == "--verify" ]]; then
  verify
  exit 0
fi

# Образ — ровно коммит: из грязного дерева выкатилось бы то, чего нет в git.
[[ -z "$(git status --porcelain --untracked-files=no)" ]] || die "в дереве незакоммиченные правки"
rev="$(git rev-parse --short=12 HEAD)"

say "· ключ KMS $key"
kid="$(idof kms symmetric-key get --name "$key")"
# Защита от удаления: удаленный ключ — это базы и финансы, которые нельзя
# расшифровать ничем. Ротация раз в год: каждая версия ключа — отдельная плата.
[[ -n "$kid" ]] || kid="$(y kms symmetric-key create --name "$key" --default-algorithm aes-256 \
  --rotation-period 8760h --deletion-protection --format json | jq -r .id)"

say "· сервисный аккаунт $sa"
sid="$(idof iam service-account get --name "$sa")"
[[ -n "$sid" ]] || sid="$(y iam service-account create --name "$sa" \
  --description "kbengine serve --tenants: only bucket $BUCKET, key $key, registry $registry" --format json | jq -r .id)"

say "· бакет $BUCKET: шифрование и доступ только $sa"
bind kms symmetric-key "$kid" kms.keys.encrypterDecrypter "$sid"
# ACL, а не роль на каталог: роль открыла бы и любой будущий бакет каталога.
# --grants заменяет ACL целиком — чужих записей в нем не остается.
"$YC" storage bucket update --name "$BUCKET" --encryption key-id="$kid" \
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
# Потолок экземпляров и запросов — потолок счета за чужие запросы.
# Метаданные: только GCE-точка, из нее код берет IAM-токен; AWS v1 закрыта.
# Секретов в окружении нет: доступ к бакету — через сервисный аккаунт.
# --command обязателен: --args платформа не дописывает к ENTRYPOINT образа,
# а запускает как команду («"serve": executable file not found»).
y serverless container revision deploy --container-name "$container" \
  --image "cr.yandex/$rid/kbengine@$digest" --service-account-id "$sid" \
  --memory 512MB --cores 1 --concurrency 8 --execution-timeout 30s \
  --zone-instances-limit 2 --zone-requests-limit 16 \
  --metadata-options aws-v1-http-endpoint=disabled,gce-http-endpoint=enabled \
  --environment TZ="$TZ_NAME" \
  --command /kbengine --args serve,--tenants,"s3://$BUCKET",--addr,0.0.0.0:8080,--allow-open >/dev/null
# Вызов открыт: окно и kbengine-mcp ходят по HTTPS, а вход — личный токен
# приложения (сервер хранит только SHA-256). Без токена — 401.
y serverless container allow-unauthenticated-invoke --name "$container" >/dev/null

verify
