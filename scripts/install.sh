#!/usr/bin/env bash
# Installs GoTab for everyday use. Safe to re-run: it upgrades in place.
set -euo pipefail

APP_NAME="GoTab"
DEST="/Applications/${APP_NAME}.app"

bold=$'\033[1m'; red=$'\033[31m'; green=$'\033[32m'; yellow=$'\033[33m'; dim=$'\033[2m'; off=$'\033[0m'
say()  { printf '%s\n' "$*"; }
ok()   { printf '%s✓%s %s\n' "$green" "$off" "$*"; }
warn() { printf '%s!%s %s\n' "$yellow" "$off" "$*"; }
die()  { printf '%s✗ %s%s\n' "$red" "$*" "$off" >&2; exit 1; }

trap 'die "Install failed on line $LINENO. Nothing was changed in /Applications."' ERR

cd "$(dirname "$0")/.."

say "${bold}Installing ${APP_NAME}${off}"
say ""

# --- checks up front, so we fail before touching anything -------------------

macos_major=$(sw_vers -productVersion | cut -d. -f1)
[ "$macos_major" -ge 11 ] || die "Needs macOS 11 or later (you have $(sw_vers -productVersion))."
ok "macOS $(sw_vers -productVersion)"

command -v go >/dev/null 2>&1 || die "Go is not installed. Get it from https://go.dev/dl/ then re-run this."
ok "Go $(go version | awk '{print $3}' | sed 's/^go//')"

xcode-select -p >/dev/null 2>&1 || die "Xcode command line tools missing. Run: xcode-select --install"
ok "Xcode command line tools"

# --- build ------------------------------------------------------------------

say ""
scripts/build.sh
say ""

# --- install ----------------------------------------------------------------

if [ -d "$DEST" ]; then
  say "Replacing the existing install…"
  # Quit a running copy first, or the replace fails with 'file busy'.
  pkill -x "$APP_NAME" 2>/dev/null || true
  sleep 1
  rm -rf "$DEST"
fi

cp -R "build/${APP_NAME}.app" "$DEST"
ok "Installed to ${DEST}"

# --- permissions ------------------------------------------------------------

say ""
say "${bold}One more step — permissions${off}"
say ""
say "A window switcher has to see and control other apps' windows, so macOS"
say "requires you to grant it two permissions by hand. This is normal and it"
say "only has to be done once."
say ""
say "  ${bold}1.${off} System Settings → Privacy & Security → ${bold}Accessibility${off}"
say "     ${dim}lets GoTab focus and move windows${off}"
say "  ${bold}2.${off} System Settings → Privacy & Security → ${bold}Screen Recording${off}"
say "     ${dim}lets GoTab show window previews${off}"
say ""
say "Add ${bold}${DEST}${off} to both lists, then launch GoTab."
say ""

if [ "${1:-}" = "--open-settings" ]; then
  open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
fi

ok "Done. Launch it with: ${bold}open ${DEST}${off}"
