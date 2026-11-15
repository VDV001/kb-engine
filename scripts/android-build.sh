#!/usr/bin/env bash
# Сборка DVK для Android (#622): подписанный bin/android/DVK.apk — TWA над /m/.
# Запускать в своем Терминале (Связка ключей спрашивает разрешение):
#   just android                      # облачный kb-сервер
#   just android kb.example.ru        # другой kb-сервер
# Первый запуск создает ключ подписи вне репозитория и кладет пароль в Связку
# ключей. Ключ не терять: без него обновление не встанет поверх приложения —
# сохраните копию dvk-release.p12 (Связка «DVK android signing» — пароль).
set -euo pipefail

HOST="${1:-bbaual33m234ntffk5a7.containers.yandexcloud.net}"
DIR="$HOME/Library/Application Support/kb-desktop/android"
KS="$DIR/dvk-release.p12"
SDK="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
GRADLE="${GRADLE:-$HOME/Library/Android/gradle/gradle-8.10.2/bin/gradle}"
export JAVA_HOME="${JAVA_HOME:-$(/usr/libexec/java_home -v 21)}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

[ -d "$SDK/platforms/android-35" ] || { echo "Нет Android SDK в $SDK (platforms;android-35, build-tools;35.0.0)"; exit 1; }
[ -x "$GRADLE" ] || { echo "Нет Gradle: $GRADLE"; exit 1; }

mkdir -p "$DIR"
chmod 700 "$DIR"
if [ ! -f "$KS" ]; then
  echo "Ключ подписи создается: $KS"
  PW="$(openssl rand -base64 32 | tr -d '/+=' | head -c 32)"
  # Пароль — через stdin и окружение, не аргументом: аргументы видны в ps.
  printf 'add-generic-password -U -s "DVK android signing" -a release -w "%s"\n' "$PW" | security -i >/dev/null
  DVK_NEW_PW="$PW" keytool -genkeypair -storetype PKCS12 -keystore "$KS" -alias dvk -keyalg RSA -keysize 3072 \
    -validity 10000 -dname "CN=DVK, O=Daniil Vdovin, C=RU" -storepass:env DVK_NEW_PW -keypass:env DVK_NEW_PW >/dev/null
  unset PW
fi
DVK_KEYSTORE_PASSWORD="$(security find-generic-password -s "DVK android signing" -a release -w)"
export DVK_KEYSTORE="$KS" DVK_KEYSTORE_PASSWORD

cd "$ROOT/mobile/android"
echo "sdk.dir=$SDK" > local.properties
VERSION="$(git -C "$ROOT" describe --tags --always)"
CODE="$(git -C "$ROOT" rev-list --count HEAD)"
"$GRADLE" --no-daemon -q assembleRelease -PdvkHost="$HOST" -PdvkVersionName="$VERSION" -PdvkVersionCode="$CODE"
mkdir -p "$ROOT/bin/android"
cp app/build/outputs/apk/release/app-release.apk "$ROOT/bin/android/DVK.apk"
"$SDK/build-tools/35.0.0/apksigner" verify "$ROOT/bin/android/DVK.apk"

FP="$(keytool -list -v -storetype PKCS12 -keystore "$KS" -storepass:env DVK_KEYSTORE_PASSWORD | awk '/SHA256:/ {print $2; exit}')"
unset DVK_KEYSTORE_PASSWORD
echo
echo "Готово: bin/android/DVK.apk ($VERSION, сервер $HOST)"
echo "Переменная kb-сервера для полноэкранного режима (выкатка):"
echo "  KB_ANDROID_APP=ru.daniilvdovin.dvk:$FP"
