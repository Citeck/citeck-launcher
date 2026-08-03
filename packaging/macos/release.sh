#!/usr/bin/env bash
# Build the macOS desktop release artifacts into dist/: the binary, a
# citeck-launcher.app bundle, and a .dmg + sha256. Requires CGO (Xcode CLT).
# The embedded web UI must already be built into internal/daemon/webdist.
#
# Env: VERSION (with or without leading "v"), ARCH (amd64|arm64).
set -euo pipefail

VERSION="${VERSION:?VERSION env required}"; VERSION="${VERSION#v}"
ARCH="${ARCH:?ARCH env required}"

mkdir -p dist/bin
CGO_ENABLED=1 GOARCH="$ARCH" go build -tags desktop \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o dist/bin/citeck-launcher ./cmd/citeck-desktop

# Step order is load-bearing: make-app.sh is the last step that MUTATES the
# bundle (it rewrites Info.plist via PlistBuddy), so signing must come after it
# and before the dmg is built from the bundle. Signing the .app after the dmg
# already exists — as the old, disabled CI snippet did — notarizes a dmg that
# still contains the unsigned app.
bash packaging/macos/make-app.sh "${VERSION}" dist/bin/citeck-launcher
bash packaging/macos/sign-app.sh
bash packaging/macos/make-dmg.sh "${VERSION}" "${ARCH}"
bash packaging/macos/notarize-dmg.sh "${VERSION}" "${ARCH}"
( cd dist && for f in *.dmg; do shasum -a 256 "$f" > "$f.sha256"; done )
echo "Built dist/citeck-desktop_${VERSION}_darwin_${ARCH}.dmg"
