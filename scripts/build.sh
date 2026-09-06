#!/usr/bin/env bash
# Builds GoTab.app into build/. Used by install.sh and by CI.
set -euo pipefail

cd "$(dirname "$0")/.."

APP_NAME="GoTab"
BUNDLE_ID="app.gotab"            # placeholder — change before any public release
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

# Universal binary so the same .app runs on Intel and Apple Silicon.
build_arch() {
  echo "    compiling ${1}"
  CGO_ENABLED=1 GOARCH="$1" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
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
    <key>CFBundleName</key>              <string>${APP_NAME}</string>
    <key>CFBundleExecutable</key>        <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>        <string>${BUNDLE_ID}</string>
    <key>CFBundleVersion</key>           <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>CFBundlePackageType</key>       <string>APPL</string>
    <key>LSMinimumSystemVersion</key>    <string>${MIN_MACOS}</string>
    <key>LSUIElement</key>               <true/>
    <key>NSHighResolutionCapable</key>   <true/>
</dict>
</plist>
PLIST

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

# Ad-hoc signature. Enough to run locally; a public release needs a Developer ID
# and notarization, or Gatekeeper will refuse it on other people's machines.
echo "    signing (ad-hoc)"
codesign --force --deep --sign - "$OUT" 2>/dev/null

echo "==> Built $OUT  (universal, macOS ${MIN_MACOS}+)"
