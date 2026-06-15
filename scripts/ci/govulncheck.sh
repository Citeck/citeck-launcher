#!/usr/bin/env bash
# CI gate: govulncheck with a committed, triaged allowlist.
#
# Runs govulncheck in symbol mode over the whole module and fails on any
# *reachable* finding (the vulnerable symbol is actually called from our
# code) whose OSV ID is not listed in govulncheck-allowlist.txt. Module- or
# package-level-only findings (no call path to the vulnerable symbol) are
# informational and do not fail the build — same policy as govulncheck's own
# human-readable output.
#
# Allowlisted IDs that no longer show up in the scan produce a warning so
# stale triage entries get cleaned up, but never fail the build (an in-flight
# dependency migration must be able to remove a vulnerability without also
# having to touch this allowlist in the same change).
set -euo pipefail
cd "$(dirname "$0")/../.."

GOVULNCHECK_VERSION=v1.3.0 # golang.org/x/vuln — keep in sync with the triage notes
ALLOWLIST=scripts/ci/govulncheck-allowlist.txt

# govulncheck builds the module, which needs internal/daemon/webdist to exist
# (the `go:embed all:webdist` directive in webui.go). On a clean checkout the web
# UI hasn't been built yet, so stub it — but only when absent, so a real build is
# never clobbered. The CI workflows create the same stub inline before this runs.
if [[ ! -f internal/daemon/webdist/index.html ]]; then
  mkdir -p internal/daemon/webdist
  echo '<html></html>' >internal/daemon/webdist/index.html
fi

echo "Running govulncheck@${GOVULNCHECK_VERSION} (symbol mode)..."
scan_json=$(go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" -json ./...)

# A finding whose trace[0].function is set means the vulnerable symbol is
# reachable from this module's code; entries without it are module/package
# level only.
reachable=$(jq -r 'select(.finding != null and .finding.trace[0].function != null) | .finding.osv' <<<"$scan_json" | sort -u)
allowed=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOWLIST" | sed 's/[[:space:]]//g' | sort -u || true)

new=$(comm -23 <(printf '%s\n' "$reachable" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))
stale=$(comm -13 <(printf '%s\n' "$reachable" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))

if [[ -n "$stale" ]]; then
  echo "WARNING: stale allowlist entries (no longer reported by govulncheck) in ${ALLOWLIST}:"
  printf '  %s\n' $stale
  echo "Remove them once the fix is confirmed."
fi

if [[ -n "$new" ]]; then
  echo "FAIL: reachable vulnerabilities not in ${ALLOWLIST}:"
  for id in $new; do
    echo "  ${id} (https://pkg.go.dev/vuln/${id})"
  done
  echo "Fix or upgrade the dependency, or triage + allowlist the ID with a justification."
  exit 1
fi

count=$(printf '%s\n' "$reachable" | sed '/^$/d' | wc -l)
echo "OK: ${count} reachable finding(s), all allowlisted; no new vulnerabilities."
