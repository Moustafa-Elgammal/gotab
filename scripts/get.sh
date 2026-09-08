#!/usr/bin/env bash
# GoTab one-line installer — downloads the latest signed release and puts it in /Applications.
#
#   curl -fsSL https://raw.githubusercontent.com/Moustafa-Elgammal/gotab/main/scripts/get.sh | bash
#   curl -fsSL https://elgx.me/gotab/install.sh | bash            # same script, served from the site
#
# It resolves the latest version from the project's own feed (no GitHub API, no jq), downloads
# GoTab-v<version>.zip plus its .sha256, VERIFIES the checksum, unpacks with ditto (which keeps the
# ad-hoc signature intact), and installs. Re-run any time to upgrade in place.
#
# Knobs (all optional):
#   GOTAB_VERSION=1.2.3          install this exact version instead of the latest
#   GOTAB_APPS="$HOME/Applications"   install here instead of /Applications
#   GOTAB_KEEP_QUARANTINE=1     do NOT strip com.apple.quarantine (you'll need right-click → Open)
#
#   ... | bash -s -- --uninstall        remove GoTab (keeps your settings)
#   ... | bash -s -- --purge            remove GoTab, its settings, and the permission grants
#                                       (--uninstall --purge is the same thing)
#
# The published bundle is ad-hoc signed, NOT notarized (see docs/DECISIONS.md D39). This script
# removes the download quarantine so it launches without a Gatekeeper prompt — that is the same trust
# you extend by piping this script to bash. Set GOTAB_KEEP_QUARANTINE=1 to opt out.
set -euo pipefail

REPO="Moustafa-Elgammal/gotab"
APP_NAME="GoTab"
FEEDS=(
  "https://elgx.me/gotab/latest.json"
  "https://moustafa-elgammal.github.io/gotab/latest.json"
)
DEST_DIR="${GOTAB_APPS:-/Applications}"
DEST="${DEST_DIR}/${APP_NAME}.app"

if [ -t 1 ]; then
  bold=$'\033[1m'; red=$'\033[31m'; green=$'\033[32m'; yellow=$'\033[33m'; dim=$'\033[2m'; off=$'\033[0m'
else
  bold=''; red=''; green=''; yellow=''; dim=''; off=''
fi
say()  { printf '%s\n' "$*"; }
ok()   { printf '%s✓%s %s\n' "$green" "$off" "$*"; }
warn() { printf '%s!%s %s\n' "$yellow" "$off" "$*"; }
die()  { printf '%s✗ %s%s\n' "$red" "$*" "$off" >&2; exit 1; }

# --- uninstall -------------------------------------------------------------------

case "${1:-}" in
--uninstall | --purge)
  say "${bold}Uninstalling ${APP_NAME}${off}"
  pkill -x "$APP_NAME" 2>/dev/null && { sleep 1; ok "Quit the running app"; } || true

  # Remove from wherever an install could have landed: GOTAB_APPS if it is set, else both the
  # system and per-user Applications — the installer auto-falls-back to ~/Applications when
  # /Applications is not writable.
  if [ -n "${GOTAB_APPS:-}" ]; then
    targets=("${GOTAB_APPS%/}/${APP_NAME}.app")
  else
    targets=("/Applications/${APP_NAME}.app" "${HOME}/Applications/${APP_NAME}.app")
  fi
  removed=0
  for t in "${targets[@]}"; do
    if [ -d "$t" ]; then
      rm -rf "$t" || die "Could not remove ${t}."
      ok "Removed ${t}"
      removed=1
    fi
  done
  [ "$removed" = 1 ] || warn "No ${APP_NAME}.app found in: ${targets[*]}"

  if [ "${1:-}" = "--purge" ] || [ "${2:-}" = "--purge" ]; then
    rm -f "${HOME}/Library/Preferences/app.gotab.plist"
    defaults delete app.gotab 2>/dev/null || true
    tccutil reset Accessibility app.gotab >/dev/null 2>&1 || true
    tccutil reset ScreenCapture app.gotab >/dev/null 2>&1 || true
    ok "Removed settings and reset the permission grants"
  fi
  say "Done."
  exit 0
  ;;
esac

# --- preflight -----------------------------------------------------------------

[ "$(uname -s)" = "Darwin" ] || die "GoTab is macOS only."
macos_major=$(sw_vers -productVersion | cut -d. -f1)
[ "$macos_major" -ge 12 ] || die "Needs macOS 12 or later (you have $(sw_vers -productVersion))."
for tool in curl ditto shasum xattr codesign; do
  command -v "$tool" >/dev/null 2>&1 || die "Required tool not found: ${tool}"
done

say "${bold}Installing ${APP_NAME}${off}"
say ""
ok "macOS $(sw_vers -productVersion)"

# --- resolve the version -----------------------------------------------------

VERSION="${GOTAB_VERSION:-}"
VERSION="${VERSION#v}"
if [ -z "$VERSION" ]; then
  for feed in "${FEEDS[@]}"; do
    json=$(curl -fsSL --retry 2 --connect-timeout 10 "$feed" 2>/dev/null) || continue
    # `|| true`: a truncated body can SIGPIPE sed via head, and `set -o pipefail` would abort the
    # whole script before the die below rather than falling through to the next feed / the die.
    VERSION=$(printf '%s' "$json" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true)
    [ -n "$VERSION" ] && break
  done
fi
if [ -z "$VERSION" ]; then
  # Last resort: the GitHub API (unauthenticated, rate-limited, but fine as a fallback). `|| true`
  # keeps a failed request (offline, DNS, 403) from aborting under `set -e` before the die.
  tag=$(curl -fsSL --retry 2 -H 'Accept: application/vnd.github+json' \
        "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true)
  VERSION="${tag#v}"
fi
[ -n "$VERSION" ] || die "Could not determine the latest version. Set GOTAB_VERSION=x.y.z and re-run."
# The version is interpolated into a download URL, so pin its shape hard.
case "$VERSION" in
  *[!0-9A-Za-z.-]* | "" ) die "Refusing a suspicious version string: '${VERSION}'" ;;
esac
ok "Latest is ${bold}v${VERSION}${off}"

# --- download + verify -----------------------------------------------------------

base="https://github.com/${REPO}/releases/download/v${VERSION}"
zip="${APP_NAME}-v${VERSION}.zip"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/gotab.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

say "Downloading ${zip} …"
curl -fL --retry 3 --connect-timeout 15 -o "${tmp}/${zip}"        "${base}/${zip}"        || die "Download failed: ${base}/${zip}"
curl -fL --retry 3 --connect-timeout 15 -o "${tmp}/${zip}.sha256" "${base}/${zip}.sha256" || die "Download failed: ${base}/${zip}.sha256"

expected=$(awk '{print $1}' "${tmp}/${zip}.sha256")
actual=$(shasum -a 256 "${tmp}/${zip}" | awk '{print $1}')
[ -n "$expected" ] || die "The published checksum file was empty."
[ "$expected" = "$actual" ] || die "Checksum mismatch — refusing to install.
  expected ${expected}
  got      ${actual}"
ok "SHA-256 verified"

ditto -x -k "${tmp}/${zip}" "${tmp}/unpack" || die "Could not unpack the archive."
src="${tmp}/unpack/${APP_NAME}.app"
[ -d "$src" ] || die "The archive did not contain ${APP_NAME}.app."
codesign --verify --strict "$src" || die "The downloaded bundle failed signature verification."
ok "Signature OK"

# --- install -------------------------------------------------------------------

if [ ! -d "$DEST_DIR" ]; then mkdir -p "$DEST_DIR" 2>/dev/null || true; fi
if ! { [ -w "$DEST_DIR" ] || { [ -d "$DEST" ] && [ -w "$DEST" ]; }; }; then
  if [ -z "${GOTAB_APPS:-}" ]; then
    DEST_DIR="${HOME}/Applications"; DEST="${DEST_DIR}/${APP_NAME}.app"
    mkdir -p "$DEST_DIR" || die "Could not create ${DEST_DIR}."
    warn "/Applications is not writable — installing to ${DEST_DIR} instead."
    warn "Re-run with GOTAB_APPS=/Applications (and sudo) for a system-wide install."
  else
    die "${DEST_DIR} is not writable."
  fi
fi

if [ -d "$DEST" ]; then
  pkill -x "$APP_NAME" 2>/dev/null || true
  sleep 1
  rm -rf "$DEST" || die "Could not remove the existing ${DEST}."
fi
ditto "$src" "$DEST" || die "Could not copy ${APP_NAME}.app into ${DEST_DIR}."

if [ -n "${GOTAB_KEEP_QUARANTINE:-}" ]; then
  warn "Left the download quarantine in place. First launch needs: right-click → Open."
else
  xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true
  say "${dim}Removed the download quarantine so it opens without a Gatekeeper prompt.${off}"
  say "${dim}The build is ad-hoc signed, not notarized (GOTAB_KEEP_QUARANTINE=1 to keep it).${off}"
fi
codesign --verify --strict "$DEST" || die "The installed copy failed signature verification."
ok "Installed ${bold}v${VERSION}${off} to ${DEST}"

# --- permissions -------------------------------------------------------------

say ""
say "${bold}One more step — permissions${off}"
say ""
say "A window switcher has to see and control other apps' windows, so macOS asks"
say "you to grant two permissions by hand. GoTab prompts for both on first launch;"
say "it only has to be done once."
say ""
say "  ${bold}1.${off} System Settings → Privacy & Security → ${bold}Accessibility${off}"
say "     ${dim}to enumerate windows and raise the one you pick${off}"
say "  ${bold}2.${off} System Settings → Privacy & Security → ${bold}Screen Recording${off}"
say "     ${dim}for window titles and the live thumbnails${off}"
say ""
ok "Launch it with: ${bold}open \"${DEST}\"${off}"
