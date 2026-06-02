#!/usr/bin/env bash
# Wrap dist/citeck-launcher.app into a .dmg with an /Applications symlink.
# Usage: make-dmg.sh <version> <arch>   (arch: amd64 | arm64)
set -euo pipefail

VERSION="${1:?usage: make-dmg.sh <version> <arch>}"
ARCH="${2:?usage: make-dmg.sh <version> <arch>}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

APP="$ROOT/dist/citeck-launcher.app"
test -d "$APP" || { echo "missing $APP — run make-app.sh first"; exit 1; }

STAGING="$(mktemp -d)"
cp -R "$APP" "$STAGING/"
ln -s /Applications "$STAGING/Applications"

OUT="$ROOT/dist/citeck-launcher_${VERSION}_darwin_${ARCH}.dmg"
rm -f "$OUT"
hdiutil create -volname "Citeck Launcher" -srcfolder "$STAGING" -ov -format UDZO "$OUT"
rm -rf "$STAGING"

echo "Built $OUT"
