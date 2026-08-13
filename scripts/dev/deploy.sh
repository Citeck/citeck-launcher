#!/bin/bash
# Quick deploy: build server binary and upload to test server.
# Usage: ./scripts/dev/deploy.sh [--restart]
#
# Credentials: see scripts/dev/ssh.sh (sourced from $CITECK_TEST_ENV_FILE).
set -e
# Without pipefail, `make build-fast | tail -1` reports tail's exit status, so a
# failed build is silently followed by an scp of whatever stale binary is still
# in dist/ — the deploy then "succeeds" and the server runs the wrong code.
set -o pipefail

CREDS_FILE="${CITECK_TEST_ENV_FILE:-$HOME/.config/citeck-launcher/test-creds.env}"
if [ -z "${CITECK_TEST_SSH_HOST:-}" ] && [ -f "$CREDS_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    source "$CREDS_FILE"
    set +a
fi
if [ -z "${CITECK_TEST_SSH_HOST:-}" ] || [ -z "${CITECK_TEST_SSH_USER:-}" ] || [ -z "${CITECK_TEST_SSH_PASS:-}" ]; then
    echo "Error: test server credentials not configured." >&2
    echo "Set CITECK_TEST_SSH_{HOST,USER,PASS} in environment or in $CREDS_FILE" >&2
    exit 1
fi

SERVER="${CITECK_TEST_SSH_USER}@${CITECK_TEST_SSH_HOST}"
SSH_OPTS="-o PreferredAuthentications=password -o PubkeyAuthentication=no -o StrictHostKeyChecking=no"
BINARY="dist/bin/citeck-server"
TARGET="/usr/local/bin/citeck"

# The repo root is two levels up: this script lives in scripts/dev/. One level
# lands in scripts/, where there is no Makefile — `make build-fast` then fails
# with "No rule to make target", scp uploads nothing, and the run still ends by
# printing the version of whatever binary is already on the server, which reads
# exactly like a successful deploy.
cd "$(dirname "$0")/../.."
if [ ! -f Makefile ]; then
    echo "Error: no Makefile in $PWD — expected the repository root." >&2
    exit 1
fi

echo "Building..."
export PATH="$HOME/go/bin:/usr/local/go/bin:$PATH"
make build-fast 2>&1 | tail -1

echo "Uploading to $SERVER:$TARGET..."
sshpass -p "$CITECK_TEST_SSH_PASS" scp $SSH_OPTS "$BINARY" "$SERVER:$TARGET"

if [ "$1" = "--restart" ]; then
    echo "Restarting daemon..."
    sshpass -p "$CITECK_TEST_SSH_PASS" ssh $SSH_OPTS "$SERVER" 'citeck stop --shutdown 2>/dev/null; sleep 2; citeck start -d'
    echo "Restarted."
fi

echo "Deployed. Version:"
sshpass -p "$CITECK_TEST_SSH_PASS" ssh $SSH_OPTS "$SERVER" 'citeck version --short'
