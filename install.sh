#!/bin/sh
set -e

# Citeck Launcher installer — minimal bootstrap.
#
# Everything beyond "fetch the binary and run it" lives inside the binary
# itself (`citeck install` handles fresh install, upgrade, and rollback
# via lifecycle detection — see internal/cli/installer_lifecycle.go).
#
# Usage:
#   curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
#   bash install.sh --rollback        # -> delegates to `citeck install --rollback`
#   bash install.sh --file ./citeck-linux-amd64
#
# Strategy:
#   1. If `citeck` is already installed and its SHA256 matches the binary
#      inside the latest release tarball, just run `citeck install` —
#      nothing to extract, the binary itself detects "already installed"
#      and hands off to the setup hint.
#   2. Otherwise download the new release tarball into a cache, verify it
#      against the published `.sha256` sidecar, extract the binary, and
#      exec `<extracted binary> install`. The binary's lifecycle code
#      copies itself to /usr/local/bin/citeck, stops the old daemon
#      preserving platform containers (v2.1.0+ clean detach or v2.0.0
#      SIGKILL fallback), swaps the binary atomically, and restarts.

REPO="Citeck/citeck-launcher"

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

    fetch_latest_version

    # Fetch the sidecar first so we can reuse a previously-cached tarball
    # without re-downloading. The sidecar is tiny (~80 bytes).
    fetch_expected_tarball_hash

    download_tarball_cached
    extract_binary

    # Compare SHA256 of the installed binary against the one we just extracted.
    # This catches rebuilds of the same version (digest changes, version doesn't).
    # We compare binary-to-binary, not tarball-to-binary — tarball hash is not
    # the same as the contained binary's hash.
    LOCAL_HASH=$(sha256_of "$(command -v citeck 2>/dev/null || true)")
    EXTRACTED_HASH=$(sha256_of "$EXTRACTED_BIN")
    if [ -n "$LOCAL_HASH" ] && [ -n "$EXTRACTED_HASH" ] && [ "$LOCAL_HASH" = "$EXTRACTED_HASH" ]; then
        log "Citeck Launcher ${VERSION} is already installed (hash match), running citeck install..."
        exec citeck install
    fi

    # exec the extracted binary. The cache path is exported via
    # CITECK_INSTALLER_CACHE so the binary can clean it up after a successful
    # install (and leave it in place on failure so a re-run reuses it).
    # We `export` explicitly rather than relying on the "VAR=val func" prefix
    # form — POSIX explicitly leaves it unspecified whether a function's
    # command inherits such assignments (issue 7, 2.9.1), so strict shells
    # like dash may not propagate it through exec sudo -E.
    export CITECK_INSTALLER_CACHE="$EXTRACTED_BIN"
    log "Running ${EXTRACTED_BIN} install..."
    exec_with_sudo "$EXTRACTED_BIN" install
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

check_deps() {
    command -v curl >/dev/null 2>&1 || err "curl is required but not installed"
    command -v tar >/dev/null 2>&1 || err "tar is required but not installed"
    command -v docker >/dev/null 2>&1 || warn "Docker is not installed — Citeck requires Docker to run"
}

# sha256_of prints the SHA256 hash of a file, or empty string if unavailable.
sha256_of() {
    [ -n "$1" ] && [ -f "$1" ] || return 0
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" 2>/dev/null | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" 2>/dev/null | awk '{print $1}'
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
        *) err "Unsupported OS: $OS (server mode supports linux only; macOS desktop build will ship separately)" ;;
    esac
    log "Platform: ${OS}/${ARCH}"
}

# fetch_latest_version resolves the newest release tag via the HTTP redirect
# of /releases/latest -> /releases/tag/vX.Y.Z. No GitHub API call, no JSON
# parsing, no anonymous rate-limit, no proxy/firewall issues. GitHub's
# /releases/latest only resolves to releases marked "Latest" — pre-releases
# (including those flagged via the API's `prerelease` bool) are excluded,
# so we don't need a manual `-*` filter here.
fetch_latest_version() {
    log "Resolving latest release..."
    LATEST_URL=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest") \
        || err "Failed to resolve latest release (network error)"
    TAG="${LATEST_URL##*/}"  # e.g. v2.1.0
    VERSION="${TAG#v}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
        err "Could not resolve latest release tag from ${LATEST_URL}"
    fi
    log "Latest version: ${VERSION}"

    TARBALL="citeck_${VERSION}_${OS}_${ARCH}.tar.gz"
    BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
}

fetch_expected_tarball_hash() {
    log "Fetching ${TARBALL}.sha256..."
    SIDECAR=$(curl -fsSL "${BASE_URL}/${TARBALL}.sha256") \
        || err "Failed to fetch ${TARBALL}.sha256. Check that release ${TAG} has an asset for ${OS}/${ARCH}"
    EXPECTED_HASH=$(printf '%s' "$SIDECAR" | awk '{print $1}')
    [ -n "$EXPECTED_HASH" ] || err "Empty SHA256 in sidecar"
}

# download_tarball_cached puts the release tarball in the installer cache.
# Cache under XDG_CACHE_HOME (default $HOME/.cache). Persists across
# install.sh invocations so repeated curl|bash runs don't re-download if
# the file is already there, and a failed install part-way through can be
# resumed without re-fetching from GitHub. If a cached file exists but its
# hash doesn't match the current sidecar, it's removed and re-downloaded.
download_tarball_cached() {
    CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/citeck-installer"
    CACHE_TARBALL="${CACHE_DIR}/${TARBALL}"
    mkdir -p "$CACHE_DIR"

    if [ -f "$CACHE_TARBALL" ]; then
        CACHED_HASH=$(sha256_of "$CACHE_TARBALL")
        if [ -n "$CACHED_HASH" ] && [ "$CACHED_HASH" = "$EXPECTED_HASH" ]; then
            log "Using cached tarball: ${CACHE_TARBALL}"
            return
        fi
        log "Cached tarball hash mismatch — re-downloading"
        rm -f "$CACHE_TARBALL"
    fi

    DOWNLOAD_URL="${BASE_URL}/${TARBALL}"
    log "Downloading ${DOWNLOAD_URL}..."

    # Download to a .tmp sibling first and rename so a half-written file
    # (network drop, Ctrl+C) can't look like a complete cached tarball
    # on the next run.
    TMP="${CACHE_TARBALL}.tmp"
    if ! curl -fL --progress-bar -o "$TMP" "$DOWNLOAD_URL"; then
        rm -f "$TMP"
        err "Download failed. Check that release ${TAG} has a binary for ${OS}/${ARCH}"
    fi

    DL_HASH=$(sha256_of "$TMP")
    if [ -z "$DL_HASH" ] || [ "$DL_HASH" != "$EXPECTED_HASH" ]; then
        rm -f "$TMP"
        err "SHA256 mismatch: expected ${EXPECTED_HASH}, got ${DL_HASH:-<unknown>}"
    fi

    mv "$TMP" "$CACHE_TARBALL"
}

# extract_binary pulls `citeck` out of the cached tarball into a sibling
# file whose name encodes the version+platform, so successive runs with
# different versions don't collide and so the binary's lifecycle code
# sees a stable path via CITECK_INSTALLER_CACHE.
extract_binary() {
    EXTRACTED_BIN="${CACHE_DIR}/citeck_${VERSION}_${OS}_${ARCH}"

    # If already extracted and hash matches the tarball's inner binary, reuse.
    # We skip rechecking the inner binary's own hash (we'd have to re-extract
    # to compare) — presence + exec bit is good enough; the tarball hash
    # already verified integrity above.
    if [ -x "$EXTRACTED_BIN" ]; then
        log "Using cached extracted binary: ${EXTRACTED_BIN}"
        return
    fi

    log "Extracting citeck from tarball..."
    TMP_EXTRACT="${EXTRACTED_BIN}.tmp"
    rm -f "$TMP_EXTRACT"
    # Extract the `citeck` entry directly to stdout so we can write it to
    # our target path without touching CACHE_DIR with arbitrary tar entries.
    if ! tar -xzOf "$CACHE_TARBALL" citeck > "$TMP_EXTRACT"; then
        rm -f "$TMP_EXTRACT"
        err "Failed to extract citeck binary from ${CACHE_TARBALL}"
    fi
    chmod +x "$TMP_EXTRACT"
    mv "$TMP_EXTRACT" "$EXTRACTED_BIN"
}

main
