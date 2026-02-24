#!/bin/bash
set -e

VERSION="{{VERSION}}"
ARCHIVE_SHA256="{{SHA256}}"
ARCHIVE_NAME="{{ARCHIVE_NAME}}"
INSTALL_DIR="/opt/citeck"
CITECK_BIN="/usr/local/bin/citeck"
OFFLINE=false

# Parse script arguments (before --)
PASSTHROUGH_ARGS=()
while [ $# -gt 0 ]; do
    case "$1" in
        --offline) OFFLINE=true; shift ;;
        *)         PASSTHROUGH_ARGS+=("$1"); shift ;;
    esac
done
set -- "${PASSTHROUGH_ARGS[@]}"

# Check if already installed and up to date
VERSION_FILE="${INSTALL_DIR}/.version"
if [ -x "${CITECK_BIN}" ] && [ -f "${VERSION_FILE}" ]; then
    INSTALLED_VERSION=$(cat "${VERSION_FILE}" 2>/dev/null || echo "")
    if [ "${INSTALLED_VERSION}" = "${VERSION}" ]; then
        exec "${CITECK_BIN}" "$@"
    fi
    echo "Citeck CLI upgrade: ${INSTALLED_VERSION} -> ${VERSION}"
    echo ""
    UPGRADE=true
else
    UPGRADE=false
fi

# Need root for install/upgrade
if [ "$(id -u)" -ne 0 ]; then
    echo "Error: this script must be run as root (sudo)." >&2
    exit 1
fi

# Check dependencies
REQUIRED_CMDS=(tar sha256sum docker)
if [ "${OFFLINE}" = false ]; then
    REQUIRED_CMDS+=(curl)
fi
missing=()
for cmd in "${REQUIRED_CMDS[@]}"; do
    if ! command -v "$cmd" &>/dev/null; then
        missing+=("$cmd")
    fi
done
if [ ${#missing[@]} -ne 0 ]; then
    echo "Error: missing required commands: ${missing[*]}" >&2
    exit 1
fi

# Resolve script directory (follow symlinks)
SOURCE="${BASH_SOURCE[0]:-$0}"
while [ -h "$SOURCE" ]; do
    DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"
    SOURCE="$(readlink "$SOURCE")"
    [[ $SOURCE != /* ]] && SOURCE="$DIR/$SOURCE"
done
SCRIPT_DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"

# Find or download archive
ARCHIVE=""

if [ -f "${SCRIPT_DIR}/${ARCHIVE_NAME}" ]; then
    echo "Found local archive: ${ARCHIVE_NAME}"
    ACTUAL=$(sha256sum "${SCRIPT_DIR}/${ARCHIVE_NAME}" | awk '{print $1}')
    if [ "${ACTUAL}" = "${ARCHIVE_SHA256}" ]; then
        ARCHIVE="${SCRIPT_DIR}/${ARCHIVE_NAME}"
        echo "Checksum OK"
    else
        if [ "${OFFLINE}" = true ]; then
            echo "Error: local archive checksum mismatch (offline mode, cannot download)." >&2
            echo "  expected: ${ARCHIVE_SHA256}" >&2
            echo "  actual:   ${ACTUAL}" >&2
            exit 1
        else
            echo "Warning: local archive checksum mismatch, downloading instead."
        fi
    fi
fi

if [ -z "${ARCHIVE}" ]; then
    if [ "${OFFLINE}" = true ]; then
        echo "Error: archive ${ARCHIVE_NAME} not found next to the script (offline mode)." >&2
        echo "Place the archive in: ${SCRIPT_DIR}/" >&2
        exit 1
    fi

    DOWNLOAD_URL="https://github.com/Citeck/citeck-launcher/releases/download/v${VERSION}/${ARCHIVE_NAME}"
    ARCHIVE=$(mktemp "/tmp/${ARCHIVE_NAME}.XXXXXX")
    trap 'rm -f "$ARCHIVE"' EXIT

    echo "Downloading ${ARCHIVE_NAME}..."
    curl -fSL -o "${ARCHIVE}" "${DOWNLOAD_URL}"

    echo "Verifying checksum..."
    ACTUAL=$(sha256sum "${ARCHIVE}" | awk '{print $1}')
    if [ "${ACTUAL}" != "${ARCHIVE_SHA256}" ]; then
        echo "Error: checksum mismatch!" >&2
        echo "  expected: ${ARCHIVE_SHA256}" >&2
        echo "  actual:   ${ACTUAL}" >&2
        exit 1
    fi
    echo "Checksum OK"
fi

# Stop daemon if upgrading
if [ "${UPGRADE}" = true ]; then
    echo "Stopping daemon..."
    "${CITECK_BIN}" stop --shutdown 2>/dev/null || true
    sleep 2
fi

# Extract
echo "Installing to ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}"
tar -xzf "${ARCHIVE}" -C "${INSTALL_DIR}" --strip-components=1

echo "${VERSION}" > "${INSTALL_DIR}/.version"
chmod +x "${INSTALL_DIR}/citeck.sh"
ln -sf "${INSTALL_DIR}/citeck.sh" "${CITECK_BIN}"

echo ""

if [ "${UPGRADE}" = true ]; then
    if systemctl is-enabled citeck &>/dev/null; then
        echo "Starting daemon..."
        systemctl start citeck
        echo "Upgrade complete: v${VERSION}"
    else
        echo "Upgrade complete: v${VERSION}"
        echo "Start the daemon with: sudo systemctl start citeck"
    fi
else
    exec "${CITECK_BIN}" "$@"
fi
