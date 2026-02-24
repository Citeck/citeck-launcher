#!/bin/bash

# Resolve symlinks to find the real script location
SOURCE="${BASH_SOURCE[0]}"
while [ -h "$SOURCE" ]; do
    DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"
    SOURCE="$(readlink "$SOURCE")"
    [[ $SOURCE != /* ]] && SOURCE="$DIR/$SOURCE"
done
BASE_DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"

# shellcheck disable=SC2086 — JAVA_OPTS intentionally unquoted (may contain multiple args)
exec "$BASE_DIR/jre/bin/java" \
    --enable-native-access=ALL-UNNAMED \
    -Dciteck.home="$BASE_DIR" \
    $JAVA_OPTS \
    -jar "$BASE_DIR/lib/citeck-cli.jar" "$@"
