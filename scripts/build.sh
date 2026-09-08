#!/usr/bin/env bash
# Builds GoTab.app into build/. Used by install.sh and by CI.
set -euo pipefail

cd "$(dirname "$0")/.."

APP_NAME="GoTab"
BUNDLE_ID="app.gotab"            # placeholder — change before any public release

# Two versions, and the distinction is Apple's. SHORT_VERSION is the marketing string
# (CFBundleShortVersionString) — dotted numbers only; hand-bumped locally, or set from the git tag by
# the release workflow via GOTAB_SHORT_VERSION (.github/workflows/release.yml, D42). VERSION is the
# build identity (CFBundleVersion, and -X main.version) — git describe, so a bug report names an
# exact commit.
SHORT_VERSION="${GOTAB_SHORT_VERSION:-0.1.0}"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")"
OUT="build/${APP_NAME}.app"

# The deployment target, in ONE place: it is stamped into the Mach-O and declared in Info.plist, and
# the two disagreeing is a bundle that says it runs on macOS 11 and then refuses to launch (D17).
#
# 12.0 is not a preference, it is Go's floor: the toolchain will not emit a lower minos, so the
# choice is 12.0 or nothing. Verified rather than assumed -- see the check after lipo.
MIN_MACOS="12.0"

# clang stamps the SDK's own version (26.0 on this host) unless told otherwise, and once real
# Objective-C is compiled clang -- not the Go linker -- decides the deployment target. Without this
# line the .app declares macOS 11 support in its plist and requires macOS 26 in its load commands.
export MACOSX_DEPLOYMENT_TARGET="${MIN_MACOS}"

echo "==> Building ${APP_NAME} ${VERSION}"

rm -rf "$OUT"
mkdir -p "$OUT/Contents/MacOS" "$OUT/Contents/Resources"

# ScreenCaptureKit must be linked WEAKLY, and only the shipped binary does it.
#
# SCK arrives in macOS 12.3 while MIN_MACOS is 12.0 (D17), so a hard link makes dyld refuse to launch
# the app at all on 12.0-12.2 -- not a missing thumbnail, a bundle that does not start. Weak linking
# resolves the framework's classes to NULL there instead, which capture.m reports as GT_ERR_UNAVAILABLE.
#
# cgo rejects -Wl,-weak_framework as an invalid LDFLAG unless CGO_LDFLAGS_ALLOW permits it, and
# exporting that for every plain `go build` would mean the gate could not run without it. So capture.go
# puts the weak link behind the `gotab_weak_sck` build tag and cgo evaluates the tag before the
# allowlist: a tagless build links hard and needs no environment, and only this script asks for both.
build_arch() {
  echo "    compiling ${1}"
  CGO_ENABLED=1 GOARCH="$1" \
    CGO_LDFLAGS_ALLOW='-Wl,-weak_framework.*' \
    go build -trimpath -tags gotab_weak_sck -ldflags "-s -w -X main.version=${VERSION}" \
    -o "build/${APP_NAME}-${1}" ./cmd/gotab
}
build_arch arm64
build_arch amd64

echo "    creating universal binary"
lipo -create -output "$OUT/Contents/MacOS/${APP_NAME}" \
  "build/${APP_NAME}-arm64" "build/${APP_NAME}-amd64"
rm -f "build/${APP_NAME}-arm64" "build/${APP_NAME}-amd64"

cat > "$OUT/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
    <key>CFBundleName</key>              <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>       <string>${APP_NAME}</string>
    <key>CFBundleExecutable</key>        <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>        <string>${BUNDLE_ID}</string>
    <key>CFBundleVersion</key>           <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${SHORT_VERSION}</string>
    <key>CFBundlePackageType</key>       <string>APPL</string>
    <key>LSMinimumSystemVersion</key>    <string>${MIN_MACOS}</string>
    <!-- Localization (P5.1): the base language, and the set that ships. AppKit reads
         Contents/Resources/<lang>.lproj/Localizable.strings; the Go side reads the
         sibling gotab.json. AllowMixedLocalizations lets an untranslated key fall
         back to the base rather than blank. -->
    <key>CFBundleDevelopmentRegion</key><string>en</string>
    <key>CFBundleAllowMixedLocalizations</key><true/>
    <key>CFBundleLocalizations</key>
    <array><string>en</string><string>de</string></array>
    <!-- Agent app: no Dock tile, no menu bar. The panel runs as Accessory and the settings window
         promotes to Regular at runtime (P4.2). -->
    <key>LSUIElement</key>               <true/>
    <key>NSHighResolutionCapable</key>   <true/>
</dict>
</plist>
PLIST

# A malformed plist is a bundle that will not launch, and a stray metacharacter in VERSION would do
# it silently. Cheap to catch here.
plutil -lint "$OUT/Contents/Info.plist" >/dev/null

# Bundle resources. resources/<lang>.lproj/ carry the ObjC .strings and the Go gotab.json for each
# locale (P5.1); resources/appcast/ carries the example release manifest (P5.3). The Go side also
# embeds en.json, so an absent resources/ tree is not fatal here -- English still works. Explicit
# `if` rather than `[ -e ] && cp` so an unmatched glob is a skip, not a `set -e` abort.
echo "    bundling resources"
for extra in resources/*.lproj resources/appcast; do
  if [ -e "$extra" ]; then cp -R "$extra" "$OUT/Contents/Resources/"; fi
done

# The plist claims MIN_MACOS; this proves the binary agrees, per slice. A mismatch is silent at build
# time and fatal at launch on the user's machine, which is the worst place to find it -- and it is a
# one-line regression away, since adding a framework or an SDK bump can move the stamp (D17).
echo "    verifying deployment target"
for arch in $(lipo -archs "$OUT/Contents/MacOS/${APP_NAME}"); do
  got=$(otool -arch "$arch" -l "$OUT/Contents/MacOS/${APP_NAME}" \
        | awk '/LC_BUILD_VERSION/{f=1} f&&/minos/{print $2; exit}')
  [ "$got" = "$MIN_MACOS" ] || {
    echo "    ERROR: ${arch} slice has minos ${got}, Info.plist declares ${MIN_MACOS}" >&2
    echo "    see docs/DECISIONS.md D17" >&2
    exit 1
  }
  echo "      ${arch}: minos ${got}"
done

# Ad-hoc signature. Enough to run locally; a public release needs a Developer ID and notarization, or
# Gatekeeper refuses it on other people's machines. No hardened runtime: ad-hoc + hardened without
# notarization buys nothing locally and can trip library validation on the weak-linked framework.
echo "    signing (ad-hoc)"
codesign --force --sign - "$OUT"
# Prove the signature took. --strict so a later nested-code addition that is not signed fails here
# rather than at first launch on someone's machine.
codesign --verify --strict "$OUT"
echo "      signature ok ($(codesign -dv "$OUT" 2>&1 | awk -F= '/^Signature/{print $2}'))"

echo "==> Built $OUT  (universal, macOS ${MIN_MACOS}+, ${SHORT_VERSION} / ${VERSION})"
