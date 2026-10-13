#!/bin/bash
# Builds build/QuickNotes.app.   ./build.sh install  also copies it to ~/Applications and opens it.
set -euo pipefail
cd "$(dirname "$0")"

echo "• config (from quicknotes.config.json)"
python3 scripts/configure.py

APP=build/QuickNotes.app
TMP=build/tmp
rm -rf "$APP" "$TMP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$TMP"

echo "• compiling desktop app"
swiftc -O -swift-version 5 -target "$(uname -m)-apple-macos13.0" \
  -o "$APP/Contents/MacOS/QuickNotes" desktop/main.swift desktop/Capture.swift desktop/Cloud.swift desktop/BubbleArt.swift desktop/Screenshot.swift desktop/Reminders.swift desktop/Voice.swift desktop/Config.swift

echo "• app icon"
swiftc -O -o "$TMP/make_icon" desktop/icon/main.swift desktop/BubbleArt.swift
"$TMP/make_icon" "$TMP/icon.png"
ICONSET="$TMP/AppIcon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$TMP/icon.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$TMP/icon.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

cp desktop/Info.plist "$APP/Contents/Info.plist"

# Ad-hoc signature with an identifier-based requirement: macOS privacy permissions (Screen Recording,
# Microphone, Speech Recognition) are tied to "local.quicknotes.app" rather than to one exact build,
# so they survive rebuilds. A Developer ID certificate is the proper fix for distributing the app.
codesign --force --deep --sign - -r='designated => identifier "local.quicknotes.app"' "$APP" >/dev/null
rm -rf "$TMP"
echo "✓ built $APP"

echo "• chrome extension"
rm -f build/quicknotes-chrome.zip
(cd extension && zip -qr ../build/quicknotes-chrome.zip . -x "preview.js")

if [[ "${1:-}" == "install" ]]; then
  EXT_DEST="$HOME/Applications/QuickNotes Chrome Extension"
  rm -rf "$EXT_DEST" && mkdir -p "$EXT_DEST"
  cp -R extension/. "$EXT_DEST/"
  echo "✓ Chrome extension copied to $EXT_DEST (chrome://extensions → Load unpacked)"

  DEST="$HOME/Applications/QuickNotes.app"
  osascript -e 'tell application id "local.quicknotes.app" to quit' >/dev/null 2>&1 || true
  sleep 1
  mkdir -p "$HOME/Applications"
  rm -rf "$DEST"
  cp -R "$APP" "$DEST"
  open "$DEST"
  echo "✓ installed to $DEST and launched"
fi
