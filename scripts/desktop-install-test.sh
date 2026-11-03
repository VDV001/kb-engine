#!/usr/bin/env bash
# Запускаемая проверка scripts/desktop-install.sh без последствий для машины:
# каталог установки и HOME во временной папке, launchctl и pkill — заглушки.
#   just desktop-install-test
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/stub" "$tmp/src/bin/kb.app/Contents/MacOS"
for c in launchctl pkill; do
  printf '#!/bin/sh\necho "%s $*" >> "%s/calls"\n[ "$1" = print ] && exit 1\nexit 0\n' "$c" "$tmp" > "$tmp/stub/$c"
  chmod +x "$tmp/stub/$c"
done
cp "$here/../desktop/build/Info.plist" "$tmp/src/bin/kb.app/Contents/Info.plist"
printf '#!/bin/sh\n' > "$tmp/src/bin/kb.app/Contents/MacOS/kb"; chmod +x "$tmp/src/bin/kb.app/Contents/MacOS/kb"
fail=0
check() { if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; fail=1; fi; }
run() { (cd "$tmp/src" && HOME="$tmp/home" PATH="$tmp/stub:$PATH" KB_APPS_DIR="$1" "$here/desktop-install.sh" "${@:2}") >/dev/null 2>&1; }

apps="$tmp/apps"
run "$apps";        check "первая установка" '[ -x "$apps/kb.app/Contents/MacOS/kb" ]'
run "$apps";        check "повторная поверх своей" '[ -x "$apps/kb.app/Contents/MacOS/kb" ]'
plist="$tmp/home/Library/LaunchAgents/com.daniilvdovin.kb.plist"
check "plist валиден" 'plutil -lint "$plist" >/dev/null'
check "агент снят и поставлен заново" 'grep -q "launchctl bootout" "$tmp/calls" && grep -q "launchctl bootstrap" "$tmp/calls"'

amp="$tmp/Apps & Tools <x>"
run "$amp";         check "путь с & и < дает валидный plist" 'plutil -lint "$plist" >/dev/null && [ "$(plutil -extract ProgramArguments.0 raw "$plist")" = "$amp/kb.app/Contents/MacOS/kb" ]'

other="$tmp/other"; mkdir -p "$other/kb.app/Contents"
printf '<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.other.kb</string></dict></plist>' > "$other/kb.app/Contents/Info.plist"
check "чужое приложение не заменяется" '! run "$other" && [ ! -e "$other/kb.app/Contents/MacOS" ]'

run "$apps" --uninstall; check "--uninstall снимает агент" '[ ! -e "$plist" ]'
exit $fail
