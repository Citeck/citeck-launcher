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

mkdir -p dist
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
  -o citeck ./cmd/citeck

TARBALL="citeck_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
tar -czf "dist/${TARBALL}" citeck
( cd dist && sha256sum "${TARBALL}" > "${TARBALL}.sha256" )
rm -f citeck
echo "Built dist/${TARBALL}"
