#!/usr/bin/env bash
# Build the Linux desktop release artifacts into dist/: the desktop binary and
# .deb + .rpm packages, each with a .sha256. Requires GTK3/WebKit dev libs (CGO)
# and nfpm on PATH. The embedded web UI must already be built into
# internal/daemon/webdist.
#
# No separate desktop auto-update payload is built here: the desktop daemon is
# the server binary, so the in-app updater downloads the server release tarball
# (citeck_<ver>_linux_<arch>.tar.gz) produced by release-server.sh.
#
# Env: VERSION (with or without leading "v"), ARCH (amd64|arm64).
set -euo pipefail

VERSION="${VERSION:?VERSION env required}"; VERSION="${VERSION#v}"
ARCH="${ARCH:?ARCH env required}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

mkdir -p dist/bin
CGO_ENABLED=1 go build -tags "desktop,gtk3" \
  -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
  -o dist/bin/citeck-launcher ./cmd/citeck-desktop

# .deb + .rpm (nfpm reads ARCH/VERSION from the environment).
export VERSION ARCH
nfpm package --config packaging/nfpm.yaml --packager deb \
  --target "dist/citeck-desktop_${VERSION}_linux_${ARCH}.deb"
nfpm package --config packaging/nfpm.yaml --packager rpm \
  --target "dist/citeck-desktop_${VERSION}_linux_${ARCH}.rpm"
( cd dist && for f in *.deb *.rpm; do sha256sum "$f" > "$f.sha256"; done )
echo "Built dist/citeck-desktop_${VERSION}_linux_${ARCH}.deb, .rpm"
