#!/usr/bin/env bash
# Wails' package-level #cgo flags follow CGO_*FLAGS. Append our target last,
# for C/C++, cgo's intermediate links and Go's external linker alike.
set -euo pipefail

target=14.0
case "${1:-}" in
  --print-target)
    printf '%s\n' "$target"
    exit 0
    ;;
  --print-command)
    case "${2:-}" in CC|CXX) ;; *) echo 'Expected CC or CXX' >&2; exit 2 ;; esac
    compiler="$(go env "$2")"
    self="$(cd "$(dirname "$0")" && pwd -P)/$(basename "$0")"
    # Go parses quoted CC/CXX itself; preserve the original command verbatim.
    # Choose a quote that the wrapper path does not contain (no shell eval).
    if [[ "$self" != *\"* ]]; then
      quoted="\"$self\""
    elif [[ "$self" != *\'* ]]; then
      quoted="'$self'"
    else
      echo 'Cannot quote macOS compiler wrapper path for Go' >&2
      exit 2
    fi
    prefix="$quoted --target=$target "
    if [[ "$compiler" == "$prefix"* ]]; then
      printf '%s\n' "$compiler" # Already set by a parent make/release.
    else
      printf '%s%s\n' "$prefix" "$compiler"
    fi
    exit 0
    ;;
  --target="$target")
    shift
    ;;
  *) echo "Expected --print-target, --print-command CC|CXX, or --target=$target compiler ..." >&2; exit 2 ;;
esac

exec "$@" "-mmacosx-version-min=$target"
