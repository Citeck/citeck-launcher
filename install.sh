#!/bin/sh
set -e

# Citeck Launcher installer — minimal bootstrap.
#
# Everything beyond "fetch the binary and run it" lives inside the binary
# itself (`citeck install` handles fresh install, upgrade, and rollback
# via lifecycle detection — see internal/cli/installer_lifecycle.go).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh | bash
#   bash install.sh --rollback        # -> delegates to `citeck install --rollback`
#   bash install.sh --file ./citeck_2.1.0_linux_amd64
#
# Strategy:
#   1. If `citeck` is already installed and its version matches the latest
#      stable v2.x release on GitHub, just run `citeck install` directly —
#      nothing to download, the binary itself detects "already installed"
#      and hands off to the setup hint.
#   2. Otherwise download the new binary to a temp location and exec
#      `<new binary> install`. The binary's lifecycle code copies itself
#      to /usr/local/bin/citeck, stops the old daemon preserving platform
#      containers (v2.1.0+ clean detach or v2.0.0 SIGKILL fallback),
#      swaps the binary atomically, and restarts the daemon.

REPO="Citeck/citeck-launcher"
VERSION_PREFIX="2."  # Only install v2.x releases

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
PASSTHROUGH=""

while [ $# -gt 0 ]; do
    case "$1" in
        --file)
            [ -z "${2:-}" ] && err "--file requires a path argument"
            FILE_PATH="$2"; shift 2 ;;
        --rollback)
            PASSTHROUGH="--rollback"; shift ;;
        *)
            err "Unknown argument: $1" ;;
    esac
done

# --- Main ---

main() {
    # --rollback doesn't need network or a new binary — the installed
    # citeck does it. Error out if not installed.
    if [ "$PASSTHROUGH" = "--rollback" ]; then
        if ! command -v citeck >/dev/null 2>&1; then
            err "citeck is not installed — nothing to rollback"
        fi
        log "Running citeck install --rollback..."
        exec citeck install --rollback
    fi

    # --file <path>: user supplied a specific binary, skip GitHub entirely.
    # No cache var — user explicitly provided the file, don't remove it.
    if [ -n "$FILE_PATH" ]; then
        [ -f "$FILE_PATH" ] || err "File not found: ${FILE_PATH}"
        [ -x "$FILE_PATH" ] || chmod +x "$FILE_PATH" 2>/dev/null || true
        log "Running ${FILE_PATH} install..."
        exec_with_sudo "$FILE_PATH" install
    fi

    check_deps
    detect_platform

    LOCAL_VERSION=$(get_local_version)
    fetch_latest_version

    # Already on the latest version — hand off to the installed binary.
    # Its lifecycle detection (self == target) will fall through to the
    # normal wizard or print the "already installed" hint.
    if [ -n "$LOCAL_VERSION" ] && [ "$LOCAL_VERSION" = "$VERSION" ]; then
        log "Citeck Launcher ${VERSION} is already installed, running citeck install..."
        exec citeck install
    fi

    # Need to install or upgrade — download into the installer cache if
    # not already there, then exec the new binary. The cache path is
    # exported via CITECK_INSTALLER_CACHE so the binary can clean it up
    # after a successful install (and leave it in place on failure so a
    # re-run of install.sh reuses the already-downloaded binary).
    # We `export` explicitly rather than relying on the "VAR=val func"
    # prefix form — POSIX explicitly leaves it unspecified whether a
    # function's command inherits such assignments (issue 7, 2.9.1), so
    # strict shells like dash may not propagate it through exec sudo -E.
    download_binary_cached
    export CITECK_INSTALLER_CACHE="$CACHE_PATH"
    log "Running ${CACHE_PATH} install..."
    exec_with_sudo "$CACHE_PATH" install
}

# exec_with_sudo runs the given binary with sudo when the process isn't
# already root (the binary needs root to write /usr/local/bin/citeck).
# With sudo -E we preserve CITECK_INSTALLER_CACHE and any other env the
# caller set before invoking us.
exec_with_sudo() {
    if [ "$(id -u)" = "0" ]; then
        exec "$@"
    else
        exec sudo -E "$@"
    fi
}

# --- Helpers ---

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

check_deps() {
    command -v curl >/dev/null 2>&1 || err "curl is required but not installed"
    command -v docker >/dev/null 2>&1 || warn "Docker is not installed — Citeck requires Docker to run"
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

    # Find the newest tag matching VERSION_PREFIX. GitHub's API returns releases
    # ordered newest-first, so the first match wins. Skip semver pre-release
    # identifiers (v2.1.0-rc1, v2.1.0-beta.1) via the '*-*' pattern — this is
    # independent of GitHub's own "prerelease" flag, which the Citeck releases
    # currently set for the entire v2.x series while the Go rewrite stabilizes.
    TAG=""
    for candidate in $(printf '%s' "$RESPONSE" | grep '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'); do
        v="${candidate#v}"
        case "$v" in
            *-*) continue ;; # skip semver pre-release identifiers
        esac
        case "$v" in
            ${VERSION_PREFIX}*)
                TAG="$candidate"
                break
                ;;
        esac
    done

    [ -z "$TAG" ] && err "No v${VERSION_PREFIX}x release found"
    VERSION="${TAG#v}"
    log "Latest version: ${VERSION}"
}

download_binary_cached() {
    # Cache under XDG_CACHE_HOME (default $HOME/.cache). Persists across
    # install.sh invocations, which means:
    #   - repeated curl|bash runs don't re-download if the file is already there
    #   - if the install fails partway through, re-running picks up the same
    #     binary instead of re-fetching from GitHub
    # The binary removes this file on successful install via the
    # CITECK_INSTALLER_CACHE env var wired in main().
    CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/citeck-installer"
    FILENAME="citeck_${VERSION}_${OS}_${ARCH}"
    CACHE_PATH="${CACHE_DIR}/${FILENAME}"

    mkdir -p "$CACHE_DIR"

    if [ -x "$CACHE_PATH" ]; then
        log "Using cached binary: ${CACHE_PATH}"
        return
    fi

    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${FILENAME}"
    log "Downloading ${DOWNLOAD_URL}..."

    # Download to a .tmp sibling first and rename into place so a half-written
    # file (network drop, Ctrl+C) can't look like a complete cached binary
    # on the next run.
    TMP="${CACHE_PATH}.tmp"
    if ! curl -fsSL -o "$TMP" "$DOWNLOAD_URL"; then
        rm -f "$TMP"
        err "Download failed. Check that release ${TAG} has a binary for ${OS}/${ARCH}"
    fi
    chmod +x "$TMP"
    mv "$TMP" "$CACHE_PATH"
}

main
