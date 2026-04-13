#!/bin/bash
# Quick deploy: build server binary and upload to test server.
# Usage: ./scripts/deploy.sh [--restart]
#
# Credentials: see scripts/ssh.sh (sourced from $CITECK_TEST_ENV_FILE).
set -e

CREDS_FILE="${CITECK_TEST_ENV_FILE:-$HOME/.claude/projects/-home-spk-IdeaProjects-citeck-launcher2/phase3-creds.env}"
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
BINARY="build/bin/citeck-server"
TARGET="/usr/local/bin/citeck"

cd "$(dirname "$0")/.."

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
