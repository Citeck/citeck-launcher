#!/bin/bash
# Full cleanup of test server: stop platform, remove all data, containers, volumes.
# Usage: ./scripts/clean-server.sh
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
SSH="sshpass -p $CITECK_TEST_SSH_PASS ssh $SSH_OPTS $SERVER"

echo "Stopping platform..."
$SSH 'citeck stop --shutdown 2>/dev/null || true'
sleep 2

echo "Removing systemd service..."
$SSH 'systemctl disable citeck 2>/dev/null; rm -f /etc/systemd/system/citeck.service; systemctl daemon-reload 2>/dev/null' || true

echo "Removing containers and volumes..."
$SSH 'docker rm -f $(docker ps -aq) 2>/dev/null; docker volume prune -af 2>/dev/null' || true

echo "Removing data and binary..."
$SSH 'rm -rf /opt/citeck /usr/local/bin/citeck /usr/local/bin/citeck.bak'

echo "Server clean."
