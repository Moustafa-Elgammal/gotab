#!/usr/bin/env bash
# Builds GoTab.app into build/. Used by install.sh and by CI.
set -euo pipefail

cd "$(dirname "$0")/.."

APP_NAME="GoTab"
BUNDLE_ID="app.gotab"            # placeholder — change before any public release
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")"
OUT="build/${APP_NAME}.app"

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
    <key>LSMinimumSystemVersion</key>    <string>11.0</string>
    <key>LSUIElement</key>               <true/>
    <key>NSHighResolutionCapable</key>   <true/>
</dict>
</plist>
PLIST

# Ad-hoc signature. Enough to run locally; a public release needs a Developer ID
# and notarization, or Gatekeeper will refuse it on other people's machines.
echo "    signing (ad-hoc)"
codesign --force --deep --sign - "$OUT" 2>/dev/null

echo "==> Built $OUT"
