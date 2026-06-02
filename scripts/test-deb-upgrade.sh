#!/usr/bin/env bash
# End-to-end test: installing the 2.* .deb over a (synthetic) 1.* .deb removes
# all old executable files and preserves the user data dir. Runs in Docker so
# the host is never modified.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

VERSION="${VERSION:-2.3.2}"
ARCH="amd64"
DIST="$REPO_ROOT/dist"
mkdir -p "$DIST"

echo "==> [1/4] Building 2.* desktop binary"
CGO_ENABLED=1 go build -tags "desktop,gtk3" \
  -ldflags "-s -w -X main.version=${VERSION}-e2e" \
  -o build/bin/citeck-launcher ./cmd/citeck-desktop

echo "==> [2/4] Packaging 2.* deb via nfpm"
command -v nfpm >/dev/null 2>&1 || go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.41.0
NFPM="$(command -v nfpm || echo "$(go env GOPATH)/bin/nfpm")"
VERSION="$VERSION" ARCH="$ARCH" "$NFPM" package --config packaging/nfpm.yaml \
  --packager deb --target "$DIST/citeck-launcher_${VERSION}_linux_${ARCH}.deb"

echo "==> [3/4] Building synthetic 1.4.1 deb (jpackage-like /opt layout)"
LEGACY="$(mktemp -d)"
mkdir -p "$LEGACY/DEBIAN" "$LEGACY/opt/citeck-launcher/bin" "$LEGACY/opt/citeck-launcher/lib/runtime"
cat > "$LEGACY/DEBIAN/control" <<EOF
Package: citeck-launcher
Version: 1.4.1
Architecture: ${ARCH}
Maintainer: Citeck LLC <info@citeck.ru>
Section: utils
Priority: optional
Description: Legacy Citeck Launcher (synthetic, for upgrade test)
EOF
printf '#!/bin/sh\necho legacy 1.4.1\n' > "$LEGACY/opt/citeck-launcher/bin/citeck-launcher"
chmod 0755 "$LEGACY/opt/citeck-launcher/bin/citeck-launcher"
echo "1.4.1" > "$LEGACY/opt/citeck-launcher/lib/runtime/release"
dpkg-deb --build --root-owner-group "$LEGACY" "$DIST/citeck-launcher_1.4.1_legacy_${ARCH}.deb"
rm -rf "$LEGACY"

echo "==> [4/4] Running upgrade scenario in ubuntu:24.04"
docker run --rm -v "$DIST:/work:ro" ubuntu:24.04 bash -euxc '
  NEW=$(ls /work/citeck-launcher_*_linux_amd64.deb)
  OLD=$(ls /work/citeck-launcher_1.4.1_legacy_amd64.deb)

  # Install legacy 1.*
  dpkg -i "$OLD"
  test -d /opt/citeck-launcher            # legacy executables present
  test -x /opt/citeck-launcher/bin/citeck-launcher

  # Seed a fake user data dir (must survive)
  mkdir -p /root/.citeck/launcher
  echo "userdata" > /root/.citeck/launcher/storage.db

  # Install runtime deps required by the 2.* deb so dpkg -i succeeds in this
  # minimal container (GTK3 + WebKit2GTK are declared in nfpm.yaml).
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    libgtk-3-0t64 libwebkit2gtk-4.1-0

  # Upgrade to 2.* (same package name -> dpkg replaces in place)
  dpkg -i "$NEW"

  # Assertions
  test ! -e /opt/citeck-launcher          || { echo "FAIL: /opt/citeck-launcher left behind"; exit 1; }
  test -x /usr/bin/citeck-launcher        || { echo "FAIL: new binary missing"; exit 1; }
  test -f /root/.citeck/launcher/storage.db || { echo "FAIL: user data removed"; exit 1; }
  grep -q userdata /root/.citeck/launcher/storage.db || { echo "FAIL: user data altered"; exit 1; }
  V=$(dpkg-query -W -f="\${Version}" citeck-launcher)
  case "$V" in 2.*) : ;; *) echo "FAIL: installed version is $V"; exit 1 ;; esac

  echo "PASS: clean upgrade — old /opt removed, new binary present, data preserved (v$V)"
'
echo "==> e2e OK"
