#!/usr/bin/env bash
# Ставит kb.app в ~/Applications и запускает его при входе в систему
# launchd-агентом — только средствами macOS (ditto, launchctl, PlistBuddy).
#
#   desktop-install.sh            поставить или обновить и запустить
#   desktop-install.sh --uninstall снять автозапуск и остановить окно (kb.app остается)
#
# KB_APPS_DIR — куда ставить (по умолчанию ~/Applications); KB_NO_LAUNCHD=1 —
# только скопировать, агент не трогать (проверка скрипта на чужом каталоге).
set -euo pipefail

label=com.daniilvdovin.kb
src=bin/kb.app
dest="${KB_APPS_DIR:-$HOME/Applications}/kb.app"
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"

# xml escapes a value for the plist: путь с & или < иначе ломал XML, и
# bootstrap падал уже после копирования (ревью #381).
# sed, а не ${s//…}: в bash 5.2 «&» в замене означает найденный текст, и
# «<» превращался в «<lt;».
xml() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

stop() {
  [[ -n "${KB_NO_LAUNCHD:-}" ]] && return
  launchctl bootout "$domain/$label" 2>/dev/null || true
  # bootout возвращается раньше, чем launchd снимет сервис: bootstrap сразу
  # после него падал с «Input/output error» (ревью #381). Ждем до 5 с.
  for _ in $(seq 50); do launchctl print "$domain/$label" >/dev/null 2>&1 || break; sleep 0.1; done
  # Окно, запущенное руками, держит ⌥Space и индекс: останавливается и оно.
  pkill -f '/kb\.app/Contents/MacOS/kb$' 2>/dev/null || true
}

if [[ "${1:-}" == "--uninstall" ]]; then
  stop
  rm -f "$plist"
  echo "kb: автозапуск снят, окно остановлено; $dest не тронут"
  exit 0
fi

[[ -d "$src" ]] || { echo "kb: нет $src — сначала just desktop" >&2; exit 1; }
# Прежний пакет заменяется, только если это наш kb.app: чужое приложение с тем
# же именем скрипт не сносит.
if [[ -e "$dest" ]]; then
  id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$dest/Contents/Info.plist" 2>/dev/null || true)
  [[ "$id" == "$label" ]] || { echo "kb: $dest — чужое приложение ($id), не заменяю" >&2; exit 1; }
fi

stop
mkdir -p "$(dirname "$dest")"
rm -rf "$dest.new"
ditto "$src" "$dest.new"
rm -rf "$dest"
mv "$dest.new" "$dest"
echo "kb: установлен $dest"

[[ -n "${KB_NO_LAUNCHD:-}" ]] && exit 0
mkdir -p "$(dirname "$plist")" "$HOME/Library/Logs"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array><string>$(xml "$dest/Contents/MacOS/kb")</string></array>
  <key>RunAtLoad</key><true/>
  <!-- Перезапуск только после падения: «Выйти из kb» завершает с кодом 0 и не перезапускается. -->
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardErrorPath</key><string>$(xml "$HOME/Library/Logs/kb.log")</string>
  <key>StandardOutPath</key><string>$(xml "$HOME/Library/Logs/kb.log")</string>
</dict>
</plist>
PLIST
launchctl bootstrap "$domain" "$plist" || { sleep 1; launchctl bootstrap "$domain" "$plist"; }
echo "kb: запускается при входе ($plist), лог — ~/Library/Logs/kb.log"
