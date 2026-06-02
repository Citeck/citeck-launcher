#!/usr/bin/env bash
# Assemble citeck-launcher.app from the built binary, Info.plist and icns.
# The bundle is named citeck-launcher.app (matching the legacy 1.* path
# /Applications/citeck-launcher.app) so a DMG drag replaces 1.* in place.
# Usage: make-app.sh <version> <path-to-citeck-launcher-binary>
set -euo pipefail

VERSION="${1:?usage: make-app.sh <version> <binary>}"
BIN="${2:?usage: make-app.sh <version> <binary>}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

APP="$ROOT/dist/citeck-launcher.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$BIN" "$APP/Contents/MacOS/citeck-launcher"
chmod +x "$APP/Contents/MacOS/citeck-launcher"
cp "$ROOT/icons/icon.icns" "$APP/Contents/Resources/appicon.icns"
cp "$ROOT/build/desktop/darwin/Info.plist" "$APP/Contents/Info.plist"

/usr/libexec/PlistBuddy -c "Set :CFBundleVersion '${VERSION}'" "$APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString '${VERSION}'" "$APP/Contents/Info.plist"

echo "Built $APP (version ${VERSION})"
