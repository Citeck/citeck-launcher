#!/bin/bash
# Run a command on the test server via SSH.
# Usage: ./scripts/ssh.sh 'citeck status'
#        ./scripts/ssh.sh   (interactive shell)
#
# Credentials: sourced from $CITECK_TEST_ENV_FILE (default:
# ~/.claude/projects/-home-spk-IdeaProjects-citeck-launcher2/phase3-creds.env).
# Required vars: CITECK_TEST_SSH_HOST, CITECK_TEST_SSH_USER, CITECK_TEST_SSH_PASS.
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

if [ $# -eq 0 ]; then
    sshpass -p "$CITECK_TEST_SSH_PASS" ssh $SSH_OPTS "$SERVER"
else
    sshpass -p "$CITECK_TEST_SSH_PASS" ssh $SSH_OPTS "$SERVER" "$@"
fi
