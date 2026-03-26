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

OS=$(detect_os)
ARCH=$(detect_arch)

echo "Detecting platform: ${OS}/${ARCH}"

# Get latest release
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
VERSION="${LATEST#v}"

echo "Downloading citeck ${VERSION}..."
URL="https://github.com/${REPO}/releases/download/${LATEST}/citeck_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSL "$URL" -o /tmp/citeck.tar.gz
tar -xzf /tmp/citeck.tar.gz -C /tmp citeck
rm /tmp/citeck.tar.gz

echo "Installing to ${INSTALL_DIR}/citeck..."
if [ -w "$INSTALL_DIR" ]; then
  mv /tmp/citeck "$INSTALL_DIR/citeck"
else
  sudo mv /tmp/citeck "$INSTALL_DIR/citeck"
fi

chmod +x "$INSTALL_DIR/citeck"
echo "Done! Run 'citeck version' to verify."
