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
trap 'rm -rf "$STAGING"' EXIT
# ditto, not `cp -R`: the bundle is code-signed by this point and cp does not
# reliably carry extended attributes / resource forks, which invalidates the seal.
ditto "$APP" "$STAGING/$(basename "$APP")"
ln -s /Applications "$STAGING/Applications"

OUT="$ROOT/dist/citeck-desktop_${VERSION}_darwin_${ARCH}.dmg"
rm -f "$OUT"
hdiutil create -volname "Citeck Launcher" -srcfolder "$STAGING" -ov -format UDZO "$OUT"
rm -rf "$STAGING"

echo "Built $OUT"
