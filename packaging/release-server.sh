#!/usr/bin/env bash
# Build the server release artifact: a static, cross-compiled binary packaged as
# a tarball + sha256, in dist/. The embedded web UI must already be built into
# internal/daemon/webdist (the `make release-server` target depends on build-web).
#
# Env: VERSION (with or without leading "v"), GOOS, GOARCH.
set -euo pipefail

VERSION="${VERSION:?VERSION env required}"; VERSION="${VERSION#v}"
GOOS="${GOOS:?GOOS env required}"
GOARCH="${GOARCH:?GOARCH env required}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The tar member carries the .exe suffix on Windows. This is hygiene, not a
# coupling: the auto-updater takes the FIRST regular file in the archive
# whatever it is called and writes it to update.DaemonBinaryName(GOOS) — that
# extracted name is the one Windows exec resolution depends on. The suffix here
# is for whoever untars the server tarball by hand.
# NOTE: a bare `[ ... ] && VAR=...` here would abort the whole script under
# `set -e` on the non-Windows branch, since the last command returns 1.
case "$GOOS" in
  windows) BIN="citeck.exe" ;;
  *)       BIN="citeck" ;;
esac

mkdir -p dist
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
  -o "$BIN" ./cmd/citeck

TARBALL="citeck_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
tar -czf "dist/${TARBALL}" "$BIN"
( cd dist && sha256sum "${TARBALL}" > "${TARBALL}.sha256" )
rm -f "$BIN"
echo "Built dist/${TARBALL} (${BIN})"
