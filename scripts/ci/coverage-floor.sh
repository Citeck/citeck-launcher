#!/usr/bin/env bash
# CI gate: per-package coverage floors for contract-critical packages.
#
# Parses the output of `go test -cover` (captured by the CI test step) and
# fails when any listed package drops below its floor. Floors are set a few
# points BELOW the measured coverage at the time they were introduced so the
# gate ratchets (catches large regressions) without blocking normal work.
#
# Baselines on 2026-06-10: appdef 91.1%, namespace 76.6%, storage 66.4%,
# daemon 38.9%. Raise a floor when a package's coverage grows past it.
set -euo pipefail

in="${1:?usage: coverage-floor.sh <captured 'go test -cover' output file>}"
mod=github.com/citeck/citeck-launcher

# package suffix -> minimum coverage percent
floors=(
  "internal/appdef:80"
  "internal/namespace:70"
  "internal/storage:60"
  "internal/daemon:35"
)

fail=0
for entry in "${floors[@]}"; do
  pkg="${entry%%:*}"
  floor="${entry##*:}"
  # "ok  	<mod>/<pkg>	12.3s	coverage: 76.6% of statements"
  pct=$(grep -E "^ok[[:space:]]+${mod}/${pkg}[[:space:]]" "$in" \
        | grep -oE 'coverage: [0-9.]+%' | grep -oE '[0-9.]+' | head -1 || true)
  if [[ -z "$pct" ]]; then
    echo "FAIL: no coverage figure for ${pkg} in ${in} (package missing or tests failed)"
    fail=1
    continue
  fi
  if awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p < f) }'; then
    echo "FAIL: ${pkg} coverage ${pct}% is below the ${floor}% floor"
    fail=1
  else
    echo "OK:   ${pkg} coverage ${pct}% (floor ${floor}%)"
  fi
done

exit "$fail"
