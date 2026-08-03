#!/usr/bin/env bash
# Code-sign dist/citeck-launcher.app.
#
# MUST run AFTER make-app.sh (which mutates Contents/Info.plist via PlistBuddy)
# and BEFORE make-dmg.sh — signing has to be the last thing that touches the
# bundle, or the seal no longer matches the contents.
#
# Env:
#   MACOS_SIGN_IDENTITY   Developer ID Application identity (name or SHA-1 hash).
#                         Empty => ad-hoc signature (see the warning below).
#   MACOS_KEYCHAIN        Optional keychain to search for the identity.
#   MACOS_REQUIRE_SIGNED  "1" => fail instead of falling back to ad-hoc.
#
# What ad-hoc buys, and why it is NOT pointless here (verified against the
# actually-shipped artifacts):
#
#   1.x   jpackage ad-hoc signed the BUNDLE -> Contents/_CodeSignature present
#         -> Gatekeeper says "developer cannot be verified", which the user can
#            bypass with right-click > Open.
#   2.9.0 make-app.sh signed nothing        -> no Contents/_CodeSignature
#         -> Gatekeeper says "is damaged and can't be opened", a hard block with
#            no in-UI recovery at all.
#
# Both were equally un-notarized; the regression is purely the missing
# bundle-level seal. (Go's linker ad-hoc signs the arm64 Mach-O automatically,
# but Gatekeeper assesses the BUNDLE, so that never helped.) Ad-hoc signing here
# restores 1.x parity. Only Developer ID + notarization removes the prompt
# entirely.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
APP="$ROOT/dist/citeck-launcher.app"
ENTITLEMENTS="$ROOT/packaging/macos/entitlements.plist"

test -d "$APP" || { echo "missing $APP — run make-app.sh first" >&2; exit 1; }

IDENTITY="${MACOS_SIGN_IDENTITY:-}"
REQUIRE="${MACOS_REQUIRE_SIGNED:-0}"

# shellcheck source=packaging/macos/keychain-args.sh
. "$(dirname "$0")/keychain-args.sh"
keychain_args_init

if [ -z "$IDENTITY" ]; then
  if [ "$REQUIRE" = "1" ]; then
    echo "::error::MACOS_SIGN_IDENTITY is not set but MACOS_REQUIRE_SIGNED=1. Refusing to ship a macOS build that Gatekeeper will reject as \"damaged\"." >&2
    exit 1
  fi
  echo "::warning::MACOS_SIGN_IDENTITY is not set — falling back to an AD-HOC signature (matching what the 1.x jpackage build shipped)."
  echo "::warning::The .dmg is NOT notarized, so first launch shows \"the developer cannot be verified\". Users open it with right-click > Open, or: xattr -dr com.apple.quarantine /Applications/citeck-launcher.app"
  # A MISSING certificate is a routine state (warned about above). codesign
  # itself FAILING is not: on a macOS runner it means a broken toolchain or a
  # malformed bundle. Continuing would publish exactly the "is damaged" artifact
  # this script exists to prevent, and a `::warning::` on an otherwise green
  # release is not something anyone reads. So this is fatal — a red build is
  # cheaper than silently shipping the bug again.
  # No --deep, for the same reason the identity path below omits it: it is
  # deprecated by Apple and this bundle has a single Mach-O with no nested code,
  # so the top-level sign is already complete. (--verify keeps --deep: verifying
  # nested code is free and catches a malformed bundle.)
  codesign --force --sign - --timestamp=none "$APP"
  codesign --verify --deep --strict --verbose=2 "$APP"
  echo "Ad-hoc signed $APP (1.x parity; still not notarized)"
  exit 0
fi

# --deep is deliberately NOT used with a real identity: it is deprecated by
# Apple and signs nested code with the outer entitlements. This bundle has no
# nested code (a single Mach-O in Contents/MacOS), so the top-level sign is
# complete on its own.
codesign --force \
  --options runtime \
  --timestamp \
  --entitlements "$ENTITLEMENTS" \
  --sign "$IDENTITY" \
  ${keychain_args[@]+"${keychain_args[@]}"} \
  "$APP"

codesign --verify --deep --strict --verbose=2 "$APP"
codesign --display --verbose=4 "$APP" 2>&1 | sed 's/^/  /'

echo "Signed $APP with '$IDENTITY'"
