#!/usr/bin/env bash
# Removes GoTab. Leaves your settings alone unless you pass --purge.
set -euo pipefail

APP_NAME="GoTab"
BUNDLE_ID="app.gotab"
DEST="${GOTAB_APPS:-/Applications}/${APP_NAME}.app"

bold=$'\033[1m'; green=$'\033[32m'; yellow=$'\033[33m'; off=$'\033[0m'
ok()   { printf '%s✓%s %s\n' "$green" "$off" "$*"; }
warn() { printf '%s!%s %s\n' "$yellow" "$off" "$*"; }

printf '%sUninstalling %s%s\n\n' "$bold" "$APP_NAME" "$off"

if pgrep -x "$APP_NAME" >/dev/null 2>&1; then
  pkill -x "$APP_NAME" 2>/dev/null || true
  sleep 1
  ok "Quit the running app"
fi

if [ -d "$DEST" ]; then
  rm -rf "$DEST"
  ok "Removed ${DEST}"
else
  warn "Not installed at ${DEST} — nothing to remove"
fi

if [ "${1:-}" = "--purge" ]; then
  rm -f "${HOME}/Library/Preferences/${BUNDLE_ID}.plist"
  defaults delete "$BUNDLE_ID" 2>/dev/null || true
  ok "Removed settings"
  # Best effort: clears GoTab's rows from the TCC database so a reinstall starts clean. Silent if it
  # was never granted, and harmless otherwise.
  if tccutil reset Accessibility "$BUNDLE_ID" >/dev/null 2>&1; then ok "Reset Accessibility grant"; fi
  if tccutil reset ScreenCapture "$BUNDLE_ID" >/dev/null 2>&1; then ok "Reset Screen Recording grant"; fi
else
  printf '\nYour settings were kept. Remove them too with: %s./scripts/uninstall.sh --purge%s\n' "$bold" "$off"
  printf 'macOS also remembers the permission grants; %s--purge%s clears those.\n' "$bold" "$off"
fi
