#!/usr/bin/env bash
# CI gate: golang.org/x/tools deadcode analysis with a committed allowlist.
#
# Analyzes the whole module with the desktop build tags (the widest build:
# server + desktop + tests all reachable) and fails on any unreachable
# function not listed in deadcode-allowlist.txt. Output lines are normalized
# by stripping the :LINE:COL position so unrelated edits don't churn the
# allowlist. Stale allowlist entries (no longer reported) warn but don't
# fail, so deleting dead code never requires a lockstep allowlist edit.
#
# Requires CGO + GTK3 dev headers for the desktop,gtk3 tags (same packages
# the CI test job installs; see .github/workflows/test.yml).
set -euo pipefail
cd "$(dirname "$0")/../.."

XTOOLS_VERSION=v0.45.0 # golang.org/x/tools — pin so CI results are reproducible
ALLOWLIST=scripts/ci/deadcode-allowlist.txt

echo "Running deadcode@${XTOOLS_VERSION} (tags: desktop,gtk3, including tests)..."
out=$(go run "golang.org/x/tools/cmd/deadcode@${XTOOLS_VERSION}" -tags desktop,gtk3 -test ./...)

# Normalize "file.go:12:6: unreachable func: F" -> "file.go: unreachable func: F".
norm=$(sed -E 's/:[0-9]+:[0-9]+:/:/' <<<"$out" | sed '/^[[:space:]]*$/d' | sort -u)
allowed=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOWLIST" | sed 's/[[:space:]]*$//' | sort -u || true)

new=$(comm -23 <(printf '%s\n' "$norm" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))
stale=$(comm -13 <(printf '%s\n' "$norm" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))

if [[ -n "$stale" ]]; then
  echo "WARNING: stale allowlist entries (no longer reported by deadcode) in ${ALLOWLIST}:"
  printf '%s\n' "$stale" | sed 's/^/  /'
  echo "Remove them — the allowlist should only ever shrink."
fi

if [[ -n "$new" ]]; then
  echo "FAIL: unreachable functions not in ${ALLOWLIST}:"
  printf '%s\n' "$new" | sed 's/^/  /'
  echo "Delete the dead code, or add an annotated allowlist entry if it must stay."
  exit 1
fi

count=$(printf '%s\n' "$norm" | sed '/^$/d' | wc -l)
echo "OK: ${count} known dead function(s), all allowlisted; no new dead code."
