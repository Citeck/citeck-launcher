#!/bin/sh
set -e

# Citeck Launcher — dev helper to pull the latest release onto a server.
#
# This is NOT the user-facing installer (see root install.sh for that).
# It's a minimal dev convenience for manual testing on a remote host:
# SSH in, run this, and the newest release binary lands in /usr/local/bin.
# It skips the daemon lifecycle (stop old daemon, container adoption,
# atomic swap, re-exec) that the real installer performs.
#
# Usage: bash scripts/pull-release.sh

REPO="Citeck/citeck-launcher"
INSTALL_DIR="/usr/local/bin"

detect_os() {
  case "$(uname -s)" in
    Linux*) echo "linux" ;;
    Darwin*) echo "darwin" ;;
    *) echo "unsupported"; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) echo "unsupported"; exit 1 ;;
  esac
}

if command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  SHA_CMD="shasum -a 256"
else
  echo "Error: neither sha256sum nor shasum found" >&2
  exit 1
fi

OS=$(detect_os)
ARCH=$(detect_arch)

echo "Detecting platform: ${OS}/${ARCH}"

# Resolve the latest tag via the HTTP redirect of /releases/latest → /releases/tag/vX.Y.Z.
# No GitHub API call (no anonymous rate-limit, no JSON parsing, no proxy/firewall issues).
LATEST_URL=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
LATEST="${LATEST_URL##*/}"   # e.g. v2.1.0
VERSION="${LATEST#v}"
if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
  echo "Error: could not resolve latest release tag" >&2
  exit 1
fi

ASSET="citeck_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${LATEST}"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading citeck ${VERSION} (${OS}/${ARCH})..."
curl -fsSL "${BASE_URL}/${ASSET}" -o "${TMP_DIR}/${ASSET}"
curl -fsSL "${BASE_URL}/${ASSET}.sha256" -o "${TMP_DIR}/${ASSET}.sha256"

echo "Verifying SHA256 checksum..."
EXPECTED=$(awk '{print $1}' "${TMP_DIR}/${ASSET}.sha256")
ACTUAL=$($SHA_CMD "${TMP_DIR}/${ASSET}" | awk '{print $1}')
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Error: SHA256 verification failed" >&2
  echo "  expected: $EXPECTED" >&2
  echo "  actual:   $ACTUAL" >&2
  exit 1
fi

echo "Extracting..."
tar -xzf "${TMP_DIR}/${ASSET}" -C "${TMP_DIR}" citeck

echo "Installing to ${INSTALL_DIR}/citeck..."
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/citeck" "$INSTALL_DIR/citeck"
  chmod +x "$INSTALL_DIR/citeck"
else
  sudo mv "${TMP_DIR}/citeck" "$INSTALL_DIR/citeck"
  sudo chmod +x "$INSTALL_DIR/citeck"
fi

echo "Done! Run 'citeck version' to verify."
