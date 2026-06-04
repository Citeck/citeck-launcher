#!/usr/bin/env bash
# Build the Windows desktop release artifacts into dist/: the .exe and a WiX MSI
# + sha256. Runs under Git Bash on the windows runner (invoked via `make
# release-desktop-windows` -> `bash`). Requires the `wix` dotnet tool on PATH.
# The embedded web UI must already be built into internal/daemon/webdist.
#
# Env: VERSION (with or without leading "v"), ARCH (amd64|arm64).
set -euo pipefail

VERSION="${VERSION:?VERSION env required}"; VERSION="${VERSION#v}"
ARCH="${ARCH:?ARCH env required}"

mkdir -p dist/bin

# Embed the app icon into the .exe via a Windows resource object. `go build`
# auto-links a matching *_windows_<arch>.syso from the main package dir; it is
# gitignored and regenerated from icons/logo.ico on every build.
go run github.com/akavel/rsrc@v0.10.2 -ico icons/logo.ico -arch "$ARCH" \
  -o "cmd/citeck-desktop/rsrc_windows_${ARCH}.syso"

CGO_ENABLED=0 GOARCH="$ARCH" go build -tags desktop \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o dist/bin/citeck-launcher.exe ./cmd/citeck-desktop

wixArch="x64"; [ "$ARCH" = "arm64" ] && wixArch="arm64"
mkdir -p dist
wix build packaging/windows/citeck-launcher.wxs -arch "$wixArch" \
  -d Version="${VERSION}" -b . -o "dist/citeck-desktop_${VERSION}_windows_${ARCH}.msi"
( cd dist && for f in *.msi; do sha256sum "$f" > "$f.sha256"; done )
echo "Built dist/citeck-desktop_${VERSION}_windows_${ARCH}.msi"
