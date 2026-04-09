#!/bin/sh
set -e

# Citeck Launcher installer
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh | bash
#   bash install.sh --file ./citeck_2.1.0_linux_amd64
#   bash install.sh --rollback

REPO="Citeck/citeck-launcher"
INSTALL_DIR="/usr/local/bin"
BINARY="citeck"
VERSION_PREFIX="2."  # Only install v2.x releases
BACKUP_SUFFIX=".bak"

# --- Utility functions (defined first for use in arg parsing) ---

log() {
    printf "  %s\n" "$1"
}

warn() {
    printf "  WARNING: %s\n" "$1" >&2
}

err() {
    printf "  ERROR: %s\n" "$1" >&2
    exit 1
}

# --- Argument parsing ---
FILE_PATH=""
ROLLBACK=false

while [ $# -gt 0 ]; do
    case "$1" in
        --file)
            [ -z "${2:-}" ] && err "--file requires a path argument"
            FILE_PATH="$2"; shift 2 ;;
        --rollback) ROLLBACK=true; shift ;;
        *) err "Unknown argument: $1" ;;
    esac
done

# --- Main ---

main() {
    TARGET="${INSTALL_DIR}/${BINARY}"

    if [ "$ROLLBACK" = true ]; then
        do_rollback
        return
    fi

    if [ -n "$FILE_PATH" ]; then
        install_from_file
        return
    fi

    install_from_github
}

# --- Install from GitHub (default) ---
install_from_github() {
    check_deps
    detect_platform

    LOCAL_VERSION=$(get_local_version)
    fetch_latest_version

    if [ -n "$LOCAL_VERSION" ] && [ "$LOCAL_VERSION" = "$VERSION" ]; then
        log "Citeck Launcher ${VERSION} is already installed, no update needed"
        log "Starting install wizard..."
        exec citeck install
    fi

    is_upgrade=false
    if [ -n "$LOCAL_VERSION" ]; then
        log "Installed version: ${LOCAL_VERSION}"
        log "Available version: ${VERSION}"
        printf "\n  Update Citeck Launcher to %s? [Y/n] " "$VERSION"
        read -r answer </dev/tty
        case "$answer" in
            [nN]*) log "Update skipped. Starting install wizard with current version..."; exec citeck install ;;
        esac
        is_upgrade=true
        stop_daemon
    else
        log "No existing installation found"
    fi

    download_binary
    backup_current
    install_binary
    log "Citeck Launcher ${VERSION} installed to ${TARGET}"

    finalize_install "$is_upgrade"
}

# --- Install from local file (--file) ---
install_from_file() {
    if [ ! -f "$FILE_PATH" ]; then
        err "File not found: ${FILE_PATH}"
    fi

    log "Installing from local file: ${FILE_PATH}"
    LOCAL_VERSION=$(get_local_version)

    is_upgrade=false
    if [ -n "$LOCAL_VERSION" ]; then
        is_upgrade=true
        stop_daemon
    fi

    TMPBIN=$(mktemp)
    cp "$FILE_PATH" "$TMPBIN"
    chmod +x "$TMPBIN"

    backup_current
    install_binary_from "$TMPBIN"

    NEW_VERSION=$(get_local_version)
    log "Citeck Launcher ${NEW_VERSION:-unknown} installed to ${TARGET}"

    finalize_install "$is_upgrade"
}

# finalize_install runs after a successful binary swap. For upgrades, it
# brings the new daemon online so the platform stays managed (containers
# kept running by detach mode are adopted by the new daemon via doStart's
# hash-matching path). For fresh installs, it hands off to the install wizard.
finalize_install() {
    is_upgrade="$1"
    if [ "$is_upgrade" = "true" ]; then
        start_daemon
        log "Upgrade complete."
        return
    fi
    log "Starting install wizard..."
    exec citeck install
}

# --- Rollback (--rollback) ---
do_rollback() {
    BACKUP="${TARGET}${BACKUP_SUFFIX}"
    if [ ! -f "$BACKUP" ]; then
        err "No backup found at ${BACKUP}. Nothing to rollback."
    fi

    log "Rolling back to previous version..."
    LOCAL_VERSION=$(get_local_version)
    stop_daemon

    if [ -w "$INSTALL_DIR" ]; then
        mv "$BACKUP" "$TARGET"
    else
        sudo mv "$BACKUP" "$TARGET"
    fi

    NEW_VERSION=$(get_local_version)
    log "Rolled back: ${LOCAL_VERSION:-unknown} → ${NEW_VERSION:-unknown}"
}

# --- Shared helpers ---

get_local_version() {
    if ! command -v citeck >/dev/null 2>&1; then
        return
    fi
    # v2.1.0+ supports --short. v2.0.0 doesn't — exits non-zero on unknown
    # flag, so we suppress that with `|| true` to keep `set -e` happy and
    # fall back to parsing the "Citeck CLI X.Y.Z" line from `citeck version`.
    v=$(citeck version --short 2>/dev/null || true)
    if [ -z "$v" ]; then
        v=$(citeck version 2>/dev/null | awk '/^Citeck CLI/ {print $NF; exit}')
    fi
    # Strip optional leading 'v' so comparisons against ${TAG#v} work uniformly.
    printf '%s' "${v#v}"
}

supports_leave_running() {
    citeck stop --help 2>&1 | grep -q -- '--leave-running'
}

stop_daemon() {
    if ! citeck status >/dev/null 2>&1; then
        return
    fi
    # Detach path (v2.1.0+): the daemon process exits but platform containers
    # stay alive. The new daemon adopts them via the runtime's hash-matching
    # path. We probe support via --help instead of running the command and
    # treating any failure as "unsupported" — that would also fall back on
    # transient HTTP errors and stop the platform unnecessarily.
    if supports_leave_running; then
        log "Detaching daemon (platform containers stay running)..."
        if citeck stop --shutdown --leave-running; then
            return
        fi
        warn "Detach failed; falling back to full shutdown"
    fi
    # v2.0.0 fallback or detach attempt failed: full shutdown stops the
    # platform too. Acceptable downtime — better than leaving the old daemon.
    log "Stopping daemon (full shutdown)..."
    citeck stop --shutdown 2>/dev/null || true
}

# start_daemon brings the new binary online after a binary swap. Uses systemd
# if a citeck.service unit is installed (the install wizard creates one with
# Restart=on-failure, so the clean detach exit does NOT auto-restart — we have
# to start it back ourselves). Otherwise falls back to detached `citeck start`.
start_daemon() {
    SERVICE_PATH="/etc/systemd/system/citeck.service"
    if [ -f "$SERVICE_PATH" ] && command -v systemctl >/dev/null 2>&1; then
        log "Starting citeck via systemd..."
        if [ "$(id -u)" = "0" ]; then
            systemctl start citeck
        else
            sudo systemctl start citeck
        fi
        return
    fi
    log "Starting citeck daemon..."
    citeck start --detach 2>/dev/null || true
}

backup_current() {
    if [ -f "$TARGET" ]; then
        log "Backing up current binary to ${TARGET}${BACKUP_SUFFIX}"
        if [ -w "$INSTALL_DIR" ]; then
            cp "$TARGET" "${TARGET}${BACKUP_SUFFIX}"
        else
            sudo cp "$TARGET" "${TARGET}${BACKUP_SUFFIX}"
        fi
    fi
}

install_binary_from() {
    SRC="$1"
    if [ -w "$INSTALL_DIR" ]; then
        mv "$SRC" "$TARGET"
    else
        log "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "$SRC" "$TARGET"
    fi
}

check_deps() {
    if ! command -v curl >/dev/null 2>&1; then
        err "curl is required but not installed"
    fi
    if ! command -v docker >/dev/null 2>&1; then
        warn "Docker is not installed — Citeck requires Docker to run"
    fi
}

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) err "Unsupported architecture: $ARCH" ;;
    esac

    case "$OS" in
        linux) ;;
        *) err "Unsupported OS: $OS (only Linux is supported)" ;;
    esac

    log "Platform: ${OS}/${ARCH}"
}

fetch_latest_version() {
    log "Fetching latest v${VERSION_PREFIX}x release..."
    RELEASES_URL="https://api.github.com/repos/${REPO}/releases?per_page=50"

    RESPONSE=$(curl -fsSL "$RELEASES_URL" 2>/dev/null) || err "Failed to fetch releases from GitHub"

    # Find the newest stable (non-prerelease) tag matching VERSION_PREFIX.
    # GitHub API returns releases sorted by creation date (newest first).
    # We filter by tag prefix and skip prereleases (prerelease: true).
    TAG=""
    for candidate in $(printf '%s' "$RESPONSE" | grep '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'); do
        v="${candidate#v}"
        case "$v" in
            ${VERSION_PREFIX}*)
                # Check this release is not a prerelease by searching for the block.
                if printf '%s' "$RESPONSE" | grep -A5 "\"tag_name\": \"${candidate}\"" | grep -q '"prerelease": false'; then
                    TAG="$candidate"
                    break
                fi
                ;;
        esac
    done

    if [ -z "$TAG" ]; then
        err "No stable v${VERSION_PREFIX}x release found"
    fi

    VERSION="${TAG#v}"
    log "Latest version: ${VERSION}"
}

download_binary() {
    FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${FILENAME}"

    log "Downloading ${DOWNLOAD_URL}..."
    TMP=$(mktemp)
    if ! curl -fsSL -o "$TMP" "$DOWNLOAD_URL"; then
        rm -f "$TMP"
        err "Download failed. Check that release ${TAG} has a binary for ${OS}/${ARCH}"
    fi

    chmod +x "$TMP"
    TMPBIN="$TMP"
}

install_binary() {
    install_binary_from "$TMPBIN"
}

main
