#!/bin/sh
set -e

# Citeck Launcher install script
# Usage: curl -fsSL https://get.citeck.ru | sh

REPO="citeck/citeck-launcher"
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

# Get latest release
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
VERSION="${LATEST#v}"

ASSET="citeck_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${LATEST}"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading citeck ${VERSION}..."
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
