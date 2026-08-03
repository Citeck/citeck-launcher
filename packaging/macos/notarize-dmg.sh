#!/usr/bin/env bash
# Sign, notarize and staple the release .dmg, then assert that Gatekeeper
# actually accepts it. Runs after make-dmg.sh.
#
# Stapling is what makes the artifact work offline and, more importantly, what
# stops the "… is damaged and can't be opened" dialog: without a stapled ticket
# macOS has to ask Apple at first launch, and a build Apple has never seen is
# rejected outright.
#
# Usage: notarize-dmg.sh <version> <arch>
# Env:
#   MACOS_SIGN_IDENTITY     Developer ID Application identity (signs the dmg too).
#   MACOS_NOTARY_APPLE_ID   Apple ID for notarytool.
#   MACOS_NOTARY_TEAM_ID    Developer Team ID.
#   MACOS_NOTARY_PASSWORD   App-specific password.
#   MACOS_KEYCHAIN          Optional keychain holding the identity.
#   MACOS_REQUIRE_SIGNED    "1" => fail when credentials are missing.
set -euo pipefail

VERSION="${1:?usage: notarize-dmg.sh <version> <arch>}"
ARCH="${2:?usage: notarize-dmg.sh <version> <arch>}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DMG="$ROOT/dist/citeck-desktop_${VERSION}_darwin_${ARCH}.dmg"

test -f "$DMG" || { echo "missing $DMG — run make-dmg.sh first" >&2; exit 1; }

IDENTITY="${MACOS_SIGN_IDENTITY:-}"
APPLE_ID="${MACOS_NOTARY_APPLE_ID:-}"
TEAM_ID="${MACOS_NOTARY_TEAM_ID:-}"
NOTARY_PASSWORD="${MACOS_NOTARY_PASSWORD:-}"
REQUIRE="${MACOS_REQUIRE_SIGNED:-0}"

if [ -z "$IDENTITY" ] || [ -z "$APPLE_ID" ] || [ -z "$TEAM_ID" ] || [ -z "$NOTARY_PASSWORD" ]; then
  if [ "$REQUIRE" = "1" ]; then
    echo "::error::Notarization credentials are incomplete but MACOS_REQUIRE_SIGNED=1. Need MACOS_SIGN_IDENTITY + MACOS_NOTARY_APPLE_ID + MACOS_NOTARY_TEAM_ID + MACOS_NOTARY_PASSWORD." >&2
    exit 1
  fi
  # Not a hard block: sign-app.sh has ad-hoc signed the bundle (1.x parity), so
  # first launch shows the bypassable "developer cannot be verified" prompt
  # rather than the unrecoverable "is damaged" one.
  echo "::warning::Skipping notarization — credentials not configured. $(basename "$DMG") will prompt \"the developer cannot be verified\" on first launch (right-click > Open to bypass)."
  exit 0
fi

# shellcheck source=packaging/macos/keychain-args.sh
. "$(dirname "$0")/keychain-args.sh"
keychain_args_init

echo "Signing $(basename "$DMG")"
codesign --force --timestamp --sign "$IDENTITY" ${keychain_args[@]+"${keychain_args[@]}"} "$DMG"

echo "Submitting $(basename "$DMG") to the notary service (this takes a few minutes)"
xcrun notarytool submit "$DMG" \
  --apple-id "$APPLE_ID" \
  --team-id "$TEAM_ID" \
  --password "$NOTARY_PASSWORD" \
  --wait

xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"

# Gate: assert what the user's Mac will actually conclude. `-t open
# --context context:primary-signature` is the assessment Finder performs on a
# quarantined disk image; `-t exec` would test the wrong policy for a .dmg.
spctl --assess --type open --context context:primary-signature --verbose=4 "$DMG"

echo "Notarized and stapled $DMG"
