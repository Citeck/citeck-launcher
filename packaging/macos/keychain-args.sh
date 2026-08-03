#!/usr/bin/env bash
# Shared construction of codesign's optional --keychain flag.
#
# Sourced (not executed) by sign-app.sh and notarize-dmg.sh. It exists so the
# empty-array guard below cannot drift between the two: the same bug was fixed
# in three places in one change set, which is exactly how it comes back.
#
# The guard matters because GitHub's macOS runners default to **bash 3.2**,
# where `"${arr[@]}"` on an EMPTY array under `set -u` aborts with "unbound
# variable". Bash 4.4+ (any Linux dev box) does not, so this is invisible
# locally and only fails on the runner. `${arr[@]+"${arr[@]}"}` expands to
# nothing when the array is empty and to the properly-quoted elements otherwise,
# and is correct on both.
#
# Usage:
#   . "$(dirname "$0")/keychain-args.sh"
#   keychain_args_init                      # reads $MACOS_KEYCHAIN
#   codesign ... $(keychain_args) ...       # NO: word-splitting
#   codesign ... "${keychain_args[@]+"${keychain_args[@]}"}" ...   # yes

keychain_args=()

keychain_args_init() {
  if [ -n "${MACOS_KEYCHAIN:-}" ]; then
    keychain_args=(--keychain "$MACOS_KEYCHAIN")
  else
    keychain_args=()
  fi
}
